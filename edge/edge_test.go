// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package edge_test

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petenewcomb/streampool"
	"github.com/petenewcomb/streampool/edge"
	"golang.org/x/net/http2"
)

// TestBothTransports fires a burst of HTTP/1.1 and HTTP/2 requests at one Server
// and verifies (a) both transports produce correct responses through the
// fasthttp and x/net/http2 paths respectively, and (b) admission is NOT
// artificially capped — requests run concurrently up to the natural bound, not
// throttled by any request-admission limiter (there is none).
func TestBothTransports(t *testing.T) {
	const burst = 12

	var cur, max int64
	app := func(_ context.Context, method, path string, _ []byte) edge.Response {
		n := atomic.AddInt64(&cur, 1)
		for { // record high-water mark
			m := atomic.LoadInt64(&max)
			if n <= m || atomic.CompareAndSwapInt64(&max, m, n) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond) // overlap so concurrency is observable
		atomic.AddInt64(&cur, -1)
		return edge.Response{Status: 200, ContentType: "text/plain", Body: []byte(method + " " + path)}
	}

	srv := edge.NewServer(app)
	srv.SetTLSConfig(serverTLS(t))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	addr := ln.Addr().String()
	h1 := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}}, //nolint:gosec
	}}
	h2 := &http.Client{Transport: &http2.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	}}

	type result struct {
		proto string
		body  string
		code  int
		err   error
	}
	results := make([]result, burst)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			client, path := h1, fmt.Sprintf("/h1/%d", i)
			if i%2 == 1 {
				client, path = h2, fmt.Sprintf("/h2/%d", i)
			}
			<-start // release all at once to force overlap
			resp, err := client.Get("https://" + addr + path)
			if err != nil {
				results[i] = result{err: err}
				return
			}
			defer func() { _ = resp.Body.Close() }()
			b, _ := io.ReadAll(resp.Body)
			results[i] = result{proto: resp.Proto, body: string(b), code: resp.StatusCode}
		}(i)
	}
	close(start)
	wg.Wait()

	// (a) correctness across both transports
	sawH1, sawH2 := false, false
	for i, r := range results {
		if r.err != nil {
			t.Fatalf("request %d failed: %v", i, r.err)
		}
		if r.code != 200 {
			t.Fatalf("request %d: status %d", i, r.code)
		}
		wantMethod, wantPath := "GET", fmt.Sprintf("/h1/%d", i)
		if i%2 == 1 {
			wantPath = fmt.Sprintf("/h2/%d", i)
		}
		if want := wantMethod + " " + wantPath; r.body != want {
			t.Fatalf("request %d: body = %q, want %q", i, r.body, want)
		}
		switch r.proto {
		case "HTTP/1.1":
			sawH1 = true
		case "HTTP/2.0":
			sawH2 = true
		default:
			t.Fatalf("request %d: unexpected proto %q", i, r.proto)
		}
	}
	if !sawH1 || !sawH2 {
		t.Fatalf("expected both transports; sawH1=%v sawH2=%v", sawH1, sawH2)
	}

	// (b) concurrency reflects the pool scaling connection tasks — not an
	// artificial admission limiter. Log the high-water mark to observe how the
	// pool absorbs connection workers blocked in the App.
	got := atomic.LoadInt64(&max)
	t.Logf("max concurrent App execution across %d requests = %d", burst, got)
	if got < 3 {
		t.Fatalf("max concurrent App execution = %d; expected the pool to run several at once", got)
	}
}

// TestH1KeepAlive verifies the fasthttp-codec read loop handles multiple
// sequential requests on ONE connection (the keep-alive path — the second and
// later iterations of serveH1's loop over the same bufio.Reader).
func TestH1KeepAlive(t *testing.T) {
	srv := edge.NewServer(func(_ context.Context, method, path string, _ []byte) edge.Response {
		return edge.Response{Status: 200, ContentType: "text/plain", Body: []byte(method + " " + path)}
	})
	srv.SetTLSConfig(serverTLS(t))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	conn, err := tls.Dial("tcp", ln.Addr().String(),
		&tls.Config{InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}}) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)

	for i := 0; i < 3; i++ {
		path := fmt.Sprintf("/ka/%d", i)
		if _, err := fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: x\r\n\r\n", path); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatalf("read %d: %v", i, err) // a codec/keep-alive bug surfaces here
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if want := "GET " + path; string(b) != want {
			t.Fatalf("request %d on shared conn: body = %q, want %q", i, b, want)
		}
	}
}

// TestFanOutLimiter shows the legitimate use of a Limiter: modeling a real
// external constraint on downstream work (e.g. "this backend allows ≤2
// concurrent calls"), NOT request admission. FanOut over many inputs respects
// the cap; the same Limiter value could be shared with other ops (HTTP apps,
// the edgegrpc service) to express a collective cap on that one dependency.
func TestFanOutLimiter(t *testing.T) {
	const limit = 2
	backend := streampool.NewSemaphore(limit)

	var cur, max int64
	fetch := func(_ context.Context, in int) (int, error) {
		n := atomic.AddInt64(&cur, 1)
		for {
			m := atomic.LoadInt64(&max)
			if n <= m || atomic.CompareAndSwapInt64(&max, m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt64(&cur, -1)
		return in * in, nil
	}

	inputs := []int{1, 2, 3, 4, 5, 6, 7, 8}
	out, err := edge.FanOut(context.Background(), backend, inputs, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(inputs) {
		t.Fatalf("got %d results, want %d", len(out), len(inputs))
	}
	if m := atomic.LoadInt64(&max); m > limit {
		t.Fatalf("downstream concurrency = %d, exceeds external constraint %d", m, limit)
	} else if m < limit {
		t.Fatalf("downstream concurrency = %d; test did not exercise the limiter (want %d)", m, limit)
	}
}

// TestH1Pipelining sends three requests back-to-back on one connection without
// reading responses in between (pipelining). Request 0 is the slowest, so it
// completes last — proving (a) responses are still delivered in request order
// (the Funnel reorder window buffered 1 and 2 until 0 finished) and (b) the
// requests processed concurrently (elapsed well under the serial sum).
func TestH1Pipelining(t *testing.T) {
	app := func(_ context.Context, method, path string, _ []byte) edge.Response {
		switch {
		case path == "/p/0":
			time.Sleep(80 * time.Millisecond) // slowest: finishes last
		default:
			time.Sleep(15 * time.Millisecond)
		}
		return edge.Response{Status: 200, ContentType: "text/plain", Body: []byte(method + " " + path)}
	}

	srv := edge.NewServer(app, edge.WithPipelining())
	srv.SetTLSConfig(serverTLS(t))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	conn, err := tls.Dial("tcp", ln.Addr().String(),
		&tls.Config{InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}}) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	// Pipeline: write all three requests up front, before reading any response.
	start := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := fmt.Fprintf(conn, "GET /p/%d HTTP/1.1\r\nHost: x\r\n\r\n", i); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	// Responses must come back in request order despite 0 finishing last.
	br := bufio.NewReader(conn)
	for i := 0; i < 3; i++ {
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if want := fmt.Sprintf("GET /p/%d", i); string(b) != want {
			t.Fatalf("response %d out of order: got %q, want %q", i, b, want)
		}
	}
	elapsed := time.Since(start)

	// Serial processing would take 80+15+15 = 110ms. Concurrent ≈ 80ms (the
	// slowest, since they overlap). Anything well under the serial sum proves
	// the requests were processed concurrently, not one-at-a-time.
	t.Logf("pipelined 3 requests (serial would be ~110ms) in %v", elapsed)
	if elapsed >= 100*time.Millisecond {
		t.Fatalf("elapsed %v: no overlap — pipelined requests processed serially", elapsed)
	}
}

// serverTLS returns a self-signed TLS config advertising both h2 and http/1.1.
func serverTLS(t *testing.T) *tls.Config {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: priv}},
		NextProtos:   []string{"h2", "http/1.1"},
	}
}
