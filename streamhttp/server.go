// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package streamhttp is a prototype of the net/http-unified edge: one standard
// http.Server serves HTTP/1.1, HTTP/2 (automatic ALPN), WebSocket (gws), and
// gRPC — on a single TLS listener, all able to fan work out to streampool. It
// replaces the fasthttp + x/net/http2 + separate-WS-front-end arrangement with
// the standard library, trading fasthttp's per-request alloc win (irrelevant to
// streampool-shaped handlers) for one front-end and standard request contexts.
package streamhttp

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/lxzan/gws"
	"github.com/petenewcomb/streampool"
	"google.golang.org/grpc"
)

const readHeaderTimeout = 10 * time.Second

// HTTPApp handles a plain (non-WS, non-gRPC) request. It runs on net/http's
// per-request goroutine — for both H/1 and H/2 — and may fan out via streampool
// sub-waves like any handler.
type HTTPApp func(ctx context.Context, r *http.Request) (status int, contentType string, body []byte)

// WSApp processes one WebSocket message and optionally returns a reply.
type WSApp func(ctx context.Context, op gws.Opcode, payload []byte) (reply []byte, replyOp gws.Opcode, send bool)

// Option configures a Server.
type Option func(*Server)

// WithOrderedWS enables ordered replies for the SUBSET of WebSocket messages for
// which ordered reports true (inspected on arrival, which is serial per conn).
// Those messages' replies are delivered in arrival order via a per-connection
// [streampool.Resequencer]; every other message's reply is written as soon as it
// finishes. Ordered and unordered replies thus coexist on one socket — and only
// the ordered subset consumes sequence numbers, so the resequencer always sees a
// gap-free run. This is the scatter-process-gather-in-order the retired HTTP/1.1
// pipelining example showed, now selectable per message on a live transport.
func WithOrderedWS(ordered func(op gws.Opcode, payload []byte) bool) Option {
	return func(s *Server) { s.orderedFn = ordered }
}

// Server is the unified front-end. gRPC dispatch is by content-type on the H/2
// path; WebSocket is the WSPath route; everything else is the HTTPApp.
type Server struct {
	httpApp   HTTPApp
	wsApp     WSApp
	grpcSrv   *grpc.Server
	wsPath    string
	orderedFn func(op gws.Opcode, payload []byte) bool

	up      *gws.Upgrader
	msgs    streampool.Wave
	process streampool.Launcher[wsInbound]
}

// wsStateKey names the per-connection ordered-reply state stored in the gws
// session (which is per-connection, so no shared map is needed).
const wsStateKey = "streamhttp.ws.resequencer"

type wsReply struct {
	op      gws.Opcode
	payload []byte // nil => this sequence produced no reply (still advances the resequencer)
}

// wsConnState is per-connection ordered-reply state.
type wsConnState struct {
	seq   uint64 // next sequence number; assigned in OnMessage (serial per conn)
	reseq streampool.Resequencer[wsReply]
}

type wsInbound struct {
	conn *gws.Conn
	msg  *gws.Message
	cs   *wsConnState // non-nil in ordered mode
	seq  uint64
}

// New builds a unified Server. wsApp and grpcSrv may be nil to disable those
// protocols.
func New(httpApp HTTPApp, wsApp WSApp, grpcSrv *grpc.Server, opts ...Option) *Server {
	s := &Server{httpApp: httpApp, wsApp: wsApp, grpcSrv: grpcSrv, wsPath: "/ws"}
	for _, opt := range opts {
		opt(s)
	}
	s.process = streampool.NewLauncher[wsInbound](
		streampool.HandlerFunc[wsInbound](func(ctx context.Context, in wsInbound, _ error) error {
			reply, op, send := s.wsApp(ctx, in.msg.Opcode, in.msg.Bytes())
			_ = in.msg.Close()
			if in.cs != nil {
				// Ordered: every sequence must reach the resequencer (a gap
				// stalls it), so submit even when there is no reply.
				var payload []byte
				if send {
					payload = reply
				}
				return in.cs.reseq.Submit(ctx, in.seq, wsReply{op: op, payload: payload})
			}
			if send {
				in.conn.WriteAsync(op, reply, nil) // unordered: write as finished
			}
			return nil
		}),
	)
	s.up = gws.NewUpgrader(&wsEvents{s: s}, &gws.ServerOption{})
	return s
}

// Handler returns the single http.Handler that multiplexes all protocols.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// gRPC rides the same H/2 listener, distinguished by content-type.
		if s.grpcSrv != nil && r.ProtoMajor == 2 &&
			strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			s.grpcSrv.ServeHTTP(w, r)
			return
		}
		// WebSocket upgrade — rides the H/1 path, hijacks the (already-TLS) conn.
		if s.wsApp != nil && r.URL.Path == s.wsPath {
			conn, err := s.up.Upgrade(w, r)
			if err != nil {
				return
			}
			s.initWS(conn) // set up ordered state before ReadLoop dispatches
			go conn.ReadLoop()
			return
		}
		// Plain HTTP — H/1 or H/2 transparently.
		status, ct, body := s.httpApp(r.Context(), r)
		if ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
}

// initWS installs the per-connection resequencer for ordered mode. Done here (not
// in OnOpen) so the state exists before ReadLoop can dispatch a message.
func (s *Server) initWS(conn *gws.Conn) {
	if s.orderedFn == nil {
		return
	}
	rw := new(streampool.Wave) // kept alive by the resequencer's internal reference
	cs := &wsConnState{}
	cs.reseq = streampool.NewFnResequencer[wsReply](rw, 0,
		func(_ context.Context, r wsReply, _ error) error {
			if r.payload != nil {
				conn.WriteAsync(r.op, r.payload, nil) // delivered in sequence order
			}
			return nil
		})
	conn.Session().Store(wsStateKey, cs)
}

// Serve runs the unified server on ln with TLS. tlsCfg's Certificates must be
// set; net/http serves HTTP/1.1 and HTTP/2 by ALPN automatically.
func (s *Server) Serve(ln net.Listener, tlsCfg *tls.Config) error {
	srv := &http.Server{
		Handler:           s.Handler(),
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: readHeaderTimeout, // Slowloris guard
	}
	return srv.ServeTLS(ln, "", "")
}

// wsEvents implements only OnMessage; the embedded BuiltinEventHandler supplies
// the rest.
type wsEvents struct {
	gws.BuiltinEventHandler
	s *Server
}

// OnMessage transfers gws's pooled message to streampool without copying; the
// worker Closes it after processing. In ordered mode it tags the message with a
// per-connection sequence number here, where delivery is serial.
func (e *wsEvents) OnMessage(conn *gws.Conn, msg *gws.Message) {
	in := wsInbound{conn: conn, msg: msg}
	// Classify on arrival (serial per conn): only ordered messages take a
	// sequence number, so the resequencer sees a gap-free run.
	if e.s.orderedFn != nil && e.s.orderedFn(msg.Opcode, msg.Bytes()) {
		if v, ok := conn.Session().Load(wsStateKey); ok {
			cs := v.(*wsConnState)
			in.cs = cs
			in.seq = cs.seq
			cs.seq++
		}
	}
	if err := e.s.process.In(&e.s.msgs).Submit(context.Background(), in); err != nil {
		_ = msg.Close()
	}
}
