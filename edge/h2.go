// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package edge

import (
	"context"
	"io"
	"net"
	"net/http"

	"golang.org/x/net/http2"
)

// serveH2 drives one HTTP/2 connection with golang.org/x/net/http2 — we do NOT
// hand-roll H/2 framing/HPACK/flow-control. ServeConn runs as the connection
// task and blocks for the connection's lifetime; x/net/http2 spawns its own
// per-stream goroutines, on which the App runs inline (and may fan out into
// streampool sub-waves like any other handler).
func (s *Server) serveH2(ctx context.Context, c net.Conn) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		resp := s.app(r.Context(), r.Method, r.URL.Path, body)
		if resp.ContentType != "" {
			w.Header().Set("Content-Type", resp.ContentType)
		}
		w.WriteHeader(resp.Status)
		_, _ = w.Write(resp.Body)
	})

	(&http2.Server{}).ServeConn(c, &http2.ServeConnOpts{
		Context: ctx,
		Handler: h,
	})
}
