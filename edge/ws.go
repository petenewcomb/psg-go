// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package edge

import (
	"context"
	"net/http"

	"github.com/lxzan/gws"
	"github.com/petenewcomb/streampool"
)

// WSServer pairs gws (a goroutine-per-connection WebSocket codec) with
// streampool. It is the consistent counterpart to the HTTP edge: a WebSocket
// connection is just a connection that kept reading after the upgrade, on the
// same standard-TLS, goroutine-per-conn model — no event loop, no forked TLS.
//
// Flow: an HTTP handler upgrades the connection (gws), then conn.ReadLoop drives
// gws's events. OnMessage hands the message to streampool WITHOUT copying: gws
// pools its message buffers and transfers ownership to us, so we Submit the
// *Message itself and Close() it on the worker after processing, which recycles
// it to gws's pool. All real work and fan-out happen on streampool; OnMessage
// stays a thin submit, which also keeps the ReadLoop goroutine's parked stack
// shallow (the memory lever for goroutine-per-conn).
//
// Replies here are unordered — each is written as its handler finishes. WebSocket
// has no wire-level request/reply coupling, so ordering is an application choice:
// for a stream that must stay ordered, feed those replies through a
// per-connection [streampool.Resequencer] whose sink writes the frames; ordered
// and unordered replies then coexist on one socket.
type WSServer struct {
	msgs    streampool.Wave
	process streampool.Launcher[wsInbound]
	app     WSApp
	up      *gws.Upgrader
}

// WSApp processes one inbound message and returns an optional reply. It runs on a
// streampool worker and may fan out into sub-waves like any handler.
//
// payload is read directly from gws's pooled buffer — there is no inbound copy —
// and is invalid once WSApp returns (the buffer is recycled), so copy anything
// you retain.
type WSApp func(ctx context.Context, op gws.Opcode, payload []byte) (reply []byte, replyOp gws.Opcode, send bool)

type wsInbound struct {
	conn *gws.Conn
	msg  *gws.Message
}

// NewWSServer builds a WSServer. Register it as an http.Handler; it speaks
// WebSocket after the upgrade. (The upgrade rides standard net/http, so it shares
// nothing with the fasthttp H/1 path — it's a separate front-end by design.)
func NewWSServer(app WSApp) *WSServer {
	s := &WSServer{app: app}
	s.process = streampool.NewLauncher[wsInbound](
		streampool.HandlerFunc[wsInbound](func(ctx context.Context, in wsInbound, _ error) error {
			reply, op, send := s.app(ctx, in.msg.Opcode, in.msg.Bytes()) // reads the pooled buffer directly
			_ = in.msg.Close()                                           // zero-copy handoff ends: recycle the inbound buffer
			if send {
				// Serialized per-conn write (frames must not interleave); safe to
				// call from concurrent workers.
				in.conn.WriteAsync(op, reply, nil)
			}
			return nil
		}),
	)
	s.up = gws.NewUpgrader(&wsEvents{s}, &gws.ServerOption{})
	return s
}

// ServeHTTP upgrades the request to WebSocket and drives it.
func (s *WSServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := s.up.Upgrade(w, r)
	if err != nil {
		return
	}
	// ReadLoop blocks for the connection's lifetime, dispatching gws events. A raw
	// goroutine keeps the example clear; it could instead be a streampool
	// connection-task (as in the HTTP edge), but WS connections are long-lived so
	// the worker-reuse benefit is a wash. Either way the per-message work is on
	// streampool, so this goroutine parks shallow between frames.
	go conn.ReadLoop()
}

type wsEvents struct{ s *WSServer }

func (e *wsEvents) OnOpen(*gws.Conn)         {}
func (e *wsEvents) OnClose(*gws.Conn, error) {}
func (e *wsEvents) OnPing(c *gws.Conn, payload []byte) {
	_ = c.WriteMessage(gws.OpcodePong, payload)
}
func (e *wsEvents) OnPong(*gws.Conn, []byte) {}

// OnMessage transfers the pooled message to streampool without copying; the
// worker reads it and then Closes it.
func (e *wsEvents) OnMessage(conn *gws.Conn, msg *gws.Message) {
	if err := e.s.process.In(&e.s.msgs).Submit(context.Background(), wsInbound{conn: conn, msg: msg}); err != nil {
		_ = msg.Close() // shutting down / cancelled: recycle now
	}
}
