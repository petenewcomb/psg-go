// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

// Package edge is a thin, Go-native HTTP edge that routes every transport's
// requests through streampool.
//
// Connections are submitted to a single connection-lifetime Wave as tasks
// (rather than a raw `go` per connection), so under high connection churn the
// adaptive pool reuses workers instead of spawning a goroutine per connection,
// and a saturated pool naturally backpressures accept. The Wave is also the
// graceful-shutdown primitive: Close + SkimAll drains live connections.
//
// Each request is processed INLINE on its connection's worker (HTTP/1.1
// requests on one connection are serial; HTTP/2 streams run on x/net/http2's
// own goroutines). The App fans its own work out into sub-waves, where real
// external constraints are expressed as shared Limiters. A request handler never
// blocks on an opaque cross-task channel, so the pool can size itself correctly.
//
// Blocking is first-class here: streampool's pool is unbounded and adaptive, so
// a connection task that spends most of its life parked in a network Read is
// exactly what the pool is built to absorb. The worker count simply tracks
// steady-state concurrency — recycled across short connections under churn, or
// grown to match however many connections are simultaneously live. (A wave drain
// can block-and-help; a hard Read block just parks a worker and the pool spawns
// another for pending work. Both are fine — there is no starvation to design
// around.)
package edge

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/petenewcomb/streampool"
)

// errServerClosed is returned by Serve after Shutdown.
var errServerClosed = errors.New("edge: server closed")

// Response is the protocol-agnostic result an [App] produces for one request.
type Response struct {
	Status      int
	ContentType string
	Body        []byte
}

// App handles one request. It runs inline on the connection's worker (HTTP/1.1)
// or an x/net/http2 stream goroutine (HTTP/2) and may fan out into sub-waves
// (see [FanOut]), where any real external constraint is a shared Limiter. Errors
// are expressed as Responses; the framework path stays error-free.
type App func(ctx context.Context, method, path string, body []byte) Response

// Server routes every transport's connections through one connection-lifetime
// Wave, which serves as the shared lifecycle and graceful-shutdown primitive.
type Server struct {
	connWave streampool.Wave               // connection-lifetime tasks: lifecycle + drain
	connOp   streampool.Launcher[net.Conn] // define-once op, reused for every connection
	app      App
	tlsCfg   *tls.Config

	baseCtx context.Context
	cancel  context.CancelFunc

	mu        sync.Mutex
	ln        net.Listener
	conns     map[net.Conn]struct{}
	acceptWg  sync.WaitGroup
	closed    bool
	pipelined bool
}

// Option configures a Server.
type Option func(*Server)

// WithPipelining enables concurrent HTTP/1.1 request pipelining: a connection
// reads ahead and dispatches each request as its own task, reordering responses
// back into request order through a per-connection Funnel (see h1pipeline.go).
// Off by default — requests are handled inline and serially. HTTP/2 is
// unaffected (x/net/http2 multiplexes streams itself).
func WithPipelining() Option { return func(s *Server) { s.pipelined = true } }

// NewServer creates a Server. Concurrency is bounded naturally (connection
// count, HTTP/2 MaxConcurrentStreams) and paced by streampool's backpressure
// from whatever real downstream constraint the App's fan-out limiters model.
// There is deliberately no admission-limiter knob.
func NewServer(app App, opts ...Option) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		app:     app,
		baseCtx: ctx,
		cancel:  cancel,
		conns:   make(map[net.Conn]struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}
	// The connection body runs on streampool's pool; it carries no Limiter
	// (nothing about "a connection was accepted" models an external constraint)
	// and is reused for every connection (ops are wave-agnostic, bound per-call
	// with In(&connWave)).
	s.connOp = streampool.NewLauncher[net.Conn](
		streampool.HandlerFunc[net.Conn](func(ctx context.Context, c net.Conn, _ error) error {
			s.handleConn(ctx, c)
			return nil
		}),
	)
	return s
}

// SetTLSConfig enables TLS. The config's NextProtos selects which transports are
// reachable, e.g. []string{"h2", "http/1.1"}. With no TLS config the server
// speaks cleartext HTTP/1.1 only.
func (s *Server) SetTLSConfig(cfg *tls.Config) { s.tlsCfg = cfg }

// Serve accepts connections until ln is closed or Shutdown is called, submitting
// each as a task to the connection Wave.
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = c.Close()
			return errServerClosed
		}
		s.acceptWg.Add(1) // pairs with Shutdown's Wait so no Submit races Close
		s.mu.Unlock()

		// Submit blocks (and the accept loop backpressures) when the pool is
		// saturated. Under churn it returns promptly onto a recycled worker.
		err = s.connOp.In(&s.connWave).Submit(s.baseCtx, c)
		s.acceptWg.Done()
		if err != nil {
			_ = c.Close()
			return err
		}
	}
}

// handleConn is the connection task body. It blocks for the connection's
// lifetime; the unbounded pool absorbs that by design — a parked Read worker is
// expected, not a problem.
func (s *Server) handleConn(ctx context.Context, c net.Conn) {
	if !s.track(c) {
		_ = c.Close()
		return
	}
	defer s.untrack(c)
	defer func() { _ = c.Close() }()

	// ctx already descends from the accept-time baseCtx; derive a per-conn
	// cancel so closing this conn unwinds its sub-waves.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if s.tlsCfg == nil {
		s.serveH1(ctx, c)
		return
	}
	tc := tls.Server(c, s.tlsCfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		return
	}
	switch tc.ConnectionState().NegotiatedProtocol {
	case "h2":
		s.serveH2(ctx, tc)
	default:
		s.serveH1(ctx, tc)
	}
}

// Shutdown stops accepting, drains live connections, or returns ctx.Err() if the
// drain ctx expires first.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	if s.ln != nil {
		_ = s.ln.Close() // unblock Accept
	}
	s.mu.Unlock()

	s.cancel()        // unblock any in-flight accept Submit; cancel conn ctxs
	s.acceptWg.Wait() // no Submit in flight and none will start → Close is safe

	s.mu.Lock()
	for c := range s.conns {
		_ = c.SetReadDeadline(time.Now()) // unblock parked Reads
	}
	s.mu.Unlock()

	s.connWave.Close()
	return s.connWave.SkimAll(ctx) // drain connection tasks; nil when empty
}

func (s *Server) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) untrack(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}
