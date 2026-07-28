// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package streamhttp_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lxzan/gws"
	"github.com/petenewcomb/streampool"
	"github.com/petenewcomb/streampool/streamgrpc"
	"github.com/petenewcomb/streampool/streamhttp"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// TestUnifiedFrontEnd proves one net/http server serves HTTP/1.1, HTTP/2,
// WebSocket, and gRPC on a single TLS listener, with
// streampool fan-out in the WS and gRPC paths.
func TestUnifiedFrontEnd(t *testing.T) {
	// gRPC service that fans out via streampool under a shared limiter.
	shared := streampool.NewSemaphore(4)
	grpcSrv := grpc.NewServer()
	streamgrpc.RegisterAggregatorServer(grpcSrv, &streamgrpc.Service{
		Gate:  shared,
		Fetch: func(_ context.Context, key string) (string, error) { return strings.ToUpper(key), nil },
	})

	srv := streamhttp.New(
		func(_ context.Context, r *http.Request) (int, string, []byte) {
			return http.StatusOK, "text/plain", []byte(r.Method + " " + r.URL.Path)
		},
		func(_ context.Context, op gws.Opcode, payload []byte) ([]byte, gws.Opcode, bool) {
			return append([]byte("echo:"), payload...), op, true
		},
		grpcSrv,
	)

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln, serverTLS(t)) }()
	addr := ln.Addr().String()
	time.Sleep(50 * time.Millisecond) // let ServeTLS start

	// 1. HTTP/1.1
	h1 := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS("http/1.1")}}
	checkHTTP(t, h1, "https://"+addr+"/h1", "HTTP/1.1")

	// 2. HTTP/2
	h2 := &http.Client{Transport: &http2.Transport{TLSClientConfig: clientTLS()}}
	checkHTTP(t, h2, "https://"+addr+"/h2", "HTTP/2.0")

	// 3. WebSocket (wss, same listener)
	checkWS(t, addr)

	// 4. gRPC (TLS h2, same listener, via grpc.Server.ServeHTTP)
	checkGRPC(t, addr)
}

// TestWSOrderedSubset is the Resequencer's edge showcase
// and shows ordering applies to only a SUBSET of a
// connection's messages. Messages prefixed "o" are ordered; "u" are not. "o0" is
// the slowest, so without resequencing its reply would arrive last — yet the "o"
// replies come back in order, while the fast "u" replies are NOT blocked behind
// the slow ordered head.
func TestWSOrderedSubset(t *testing.T) {
	app := func(_ context.Context, op gws.Opcode, payload []byte) ([]byte, gws.Opcode, bool) {
		reply := append([]byte(nil), payload...) // copy: payload is recycled after we return
		if string(payload) == "o0" {
			time.Sleep(100 * time.Millisecond) // slowest, and it heads the ordered stream
		} else {
			time.Sleep(15 * time.Millisecond)
		}
		return reply, op, true
	}
	ordered := func(_ gws.Opcode, payload []byte) bool {
		return len(payload) > 0 && payload[0] == 'o'
	}
	srv := streamhttp.New(nil, app, nil, streamhttp.WithOrderedWS(ordered))

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln, serverTLS(t)) }()
	addr := ln.Addr().String()
	time.Sleep(50 * time.Millisecond)

	got := make(chan string, 16)
	client, resp, err := gws.NewClient(&wsClient{got: got}, &gws.ClientOption{
		Addr:      "wss://" + addr + "/ws",
		TlsConfig: clientTLS(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	go client.ReadLoop()
	defer func() { _ = client.WriteClose(1000, nil) }()

	send := []string{"o0", "u0", "o1", "u1", "o2", "u2"}
	start := time.Now()
	for _, m := range send {
		if err := client.WriteString(m); err != nil {
			t.Fatalf("write %s: %v", m, err)
		}
	}

	arrival := make([]string, 0, len(send))
	for range send {
		select {
		case s := <-got:
			arrival = append(arrival, s)
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out after %d replies: %v", len(arrival), arrival)
		}
	}
	elapsed := time.Since(start)
	t.Logf("arrival order: %v (in %v)", arrival, elapsed)

	// The ordered subset arrives in order.
	var ord []string
	o0idx := -1
	for i, s := range arrival {
		if strings.HasPrefix(s, "o") {
			ord = append(ord, s)
		}
		if s == "o0" {
			o0idx = i
		}
	}
	if strings.Join(ord, ",") != "o0,o1,o2" {
		t.Fatalf("ordered subset arrived %v, want [o0 o1 o2]", ord)
	}
	// Unordered replies are not blocked behind the slow ordered head: each "u"
	// arrives before o0.
	for i, s := range arrival {
		if strings.HasPrefix(s, "u") && i > o0idx {
			t.Fatalf("unordered %q arrived after the slow o0 (blocked); arrival: %v", s, arrival)
		}
	}
	// Overlap: serial would be 100 + 5*15 = 175ms; concurrent ≈ 100ms.
	if elapsed >= 160*time.Millisecond {
		t.Fatalf("no overlap: %v", elapsed)
	}
}

func checkHTTP(t *testing.T, c *http.Client, url, wantProto string) {
	t.Helper()
	resp, err := c.Get(url) //nolint:noctx // test
	if err != nil {
		t.Fatalf("%s GET: %v", wantProto, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.Proto != wantProto {
		t.Fatalf("proto = %q, want %q", resp.Proto, wantProto)
	}
	path := url[strings.LastIndex(url, "/"):]
	if want := "GET " + path; string(b) != want {
		t.Fatalf("%s body = %q, want %q", wantProto, b, want)
	}
}

func checkWS(t *testing.T, addr string) {
	t.Helper()
	got := make(chan string, 4)
	client, resp, err := gws.NewClient(&wsClient{got: got}, &gws.ClientOption{
		Addr:      "wss://" + addr + "/ws",
		TlsConfig: clientTLS(),
	})
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	go client.ReadLoop()
	defer func() { _ = client.WriteClose(1000, nil) }()

	if err := client.WriteString("hi"); err != nil {
		t.Fatalf("ws write: %v", err)
	}
	select {
	case s := <-got:
		if s != "echo:hi" {
			t.Fatalf("ws reply = %q, want %q", s, "echo:hi")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ws: timed out")
	}
}

func checkGRPC(t *testing.T, addr string) {
	t.Helper()
	creds := credentials.NewTLS(clientTLS("h2"))
	conn, err := grpc.NewClient("passthrough:///"+addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		t.Fatalf("grpc dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	var out streamgrpc.AggregateResponse
	err = conn.Invoke(context.Background(), streamgrpc.AggregateFullMethod,
		&streamgrpc.AggregateRequest{Keys: []string{"a", "b", "c"}}, &out,
		grpc.CallContentSubtype(streamgrpc.CodecName))
	if err != nil {
		t.Fatalf("grpc Invoke: %v", err)
	}
	sort.Strings(out.Values)
	if strings.Join(out.Values, ",") != "A,B,C" {
		t.Fatalf("grpc result = %v, want [A B C]", out.Values)
	}
}

type wsClient struct{ got chan string }

func (c *wsClient) OnOpen(*gws.Conn)         {}
func (c *wsClient) OnClose(*gws.Conn, error) {}
func (c *wsClient) OnPing(*gws.Conn, []byte) {}
func (c *wsClient) OnPong(*gws.Conn, []byte) {}
func (c *wsClient) OnMessage(_ *gws.Conn, m *gws.Message) {
	c.got <- string(m.Bytes())
	_ = m.Close()
}

func clientTLS(protos ...string) *tls.Config {
	return &tls.Config{InsecureSkipVerify: true, NextProtos: protos} //nolint:gosec // test
}

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
