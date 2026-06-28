// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package edgegrpc_test

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/streampool"
	"github.com/petenewcomb/streampool/edgegrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestSharedGateAcrossRPCs runs a real gRPC server (grpc-go owns the HTTP/2
// transport) whose method fans out through streampool. It verifies that one
// shared streampool.Limiter — modeling a downstream external constraint — caps
// fan-out concurrency collectively across all concurrent RPCs. The same Limiter
// value could also back an edge.App's FanOut, giving HTTP and gRPC callers one
// collective cap on the shared dependency.
func TestSharedGateAcrossRPCs(t *testing.T) {
	const gate = 2
	const rpcs = 4
	const keysPerRPC = 4

	shared := streampool.NewSemaphore(gate)

	var cur, peak int64
	fetch := func(_ context.Context, key string) (string, error) {
		n := atomic.AddInt64(&cur, 1)
		for {
			m := atomic.LoadInt64(&peak)
			if n <= m || atomic.CompareAndSwapInt64(&peak, m, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt64(&cur, -1)
		return strings.ToUpper(key), nil
	}

	lis, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer()
	edgegrpc.RegisterAggregatorServer(gs, &edgegrpc.Service{Gate: shared, Fetch: fetch})
	go func() { _ = gs.Serve(lis) }()
	defer gs.Stop()

	conn, err := grpc.NewClient(
		"passthrough:///"+lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	// Fire several RPCs concurrently; each fans out keysPerRPC downstream calls.
	keysFor := func(r int) []string {
		ks := make([]string, keysPerRPC)
		for k := range ks {
			ks[k] = fmt.Sprintf("%c%d", 'a'+r, k)
		}
		return ks
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, rpcs)
	got := make([][]string, rpcs)
	for r := 0; r < rpcs; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			<-start
			var resp edgegrpc.AggregateResponse
			err := conn.Invoke(
				context.Background(),
				edgegrpc.AggregateFullMethod,
				&edgegrpc.AggregateRequest{Keys: keysFor(r)},
				&resp,
				grpc.CallContentSubtype(edgegrpc.CodecName),
			)
			errs[r], got[r] = err, resp.Values
		}(r)
	}
	close(start)
	wg.Wait()

	// Correctness: each RPC returns the uppercased keys it sent (any order).
	for r := 0; r < rpcs; r++ {
		if errs[r] != nil {
			t.Fatalf("rpc %d: %v", r, errs[r])
		}
		want := keysFor(r)
		for i := range want {
			want[i] = strings.ToUpper(want[i])
		}
		sort.Strings(want)
		sort.Strings(got[r])
		if strings.Join(want, ",") != strings.Join(got[r], ",") {
			t.Fatalf("rpc %d: got %v, want %v", r, got[r], want)
		}
	}

	// The shared gate capped fan-out concurrency across ALL rpcs collectively.
	if m := atomic.LoadInt64(&peak); m > gate {
		t.Fatalf("max concurrent fetch = %d, exceeds shared gate %d", m, gate)
	} else if m < gate {
		t.Fatalf("max concurrent fetch = %d; test did not exercise the gate (want %d)", m, gate)
	}
}
