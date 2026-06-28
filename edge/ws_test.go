// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package edge_test

import (
	"context"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lxzan/gws"
	"github.com/petenewcomb/streampool/edge"
)

// TestWSEcho exercises the gws + streampool path end to end: a client opens a
// WebSocket, sends a burst of messages, and each is upgraded → ReadLoop →
// OnMessage → Submit to streampool → handler → reply. Replies are unordered
// (concurrent processing), so they are checked as a set.
func TestWSEcho(t *testing.T) {
	srv := edge.NewWSServer(func(_ context.Context, op gws.Opcode, payload []byte) ([]byte, gws.Opcode, bool) {
		return append([]byte("echo:"), payload...), op, true
	})
	hs := httptest.NewServer(srv)
	defer hs.Close()

	got := make(chan string, 32)
	client, resp, err := gws.NewClient(&wsClient{got: got}, &gws.ClientOption{
		Addr: "ws" + strings.TrimPrefix(hs.URL, "http"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	go client.ReadLoop()
	defer func() { _ = client.WriteClose(1000, nil) }()

	const n = 8
	for i := 0; i < n; i++ {
		if err := client.WriteString(strconv.Itoa(i)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	want := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		want["echo:"+strconv.Itoa(i)] = true
	}
	for i := 0; i < n; i++ {
		select {
		case s := <-got:
			if !want[s] {
				t.Fatalf("unexpected reply %q", s)
			}
			delete(want, s)
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out; missing replies: %v", want)
		}
	}
}

// wsClient collects inbound messages for the test.
type wsClient struct{ got chan string }

func (c *wsClient) OnOpen(*gws.Conn)         {}
func (c *wsClient) OnClose(*gws.Conn, error) {}
func (c *wsClient) OnPing(*gws.Conn, []byte) {}
func (c *wsClient) OnPong(*gws.Conn, []byte) {}
func (c *wsClient) OnMessage(_ *gws.Conn, msg *gws.Message) {
	c.got <- string(msg.Bytes())
	_ = msg.Close()
}
