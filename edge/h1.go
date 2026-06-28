// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package edge

import (
	"bufio"
	"context"
	"net"

	"github.com/valyala/fasthttp"
)

// serveH1 dispatches to the inline (default) or pipelined HTTP/1.1 path.
func (s *Server) serveH1(ctx context.Context, c net.Conn) {
	if s.pipelined {
		s.serveH1Pipelined(ctx, c)
		return
	}
	s.serveH1Inline(ctx, c)
}

// serveH1Inline drives one HTTP/1.1 connection using fasthttp's Request/Response
// as a standalone codec (no fasthttp.Server). It runs on the connection's pool
// worker; requests are handled inline and in order (correct for HTTP/1.1).
func (s *Server) serveH1Inline(ctx context.Context, c net.Conn) {
	br := bufio.NewReader(c)
	bw := bufio.NewWriter(c)
	for {
		req := fasthttp.AcquireRequest()
		// Blocks (parked on the runtime netpoller) until a full request or EOF.
		if err := req.Read(br); err != nil {
			fasthttp.ReleaseRequest(req)
			return
		}
		method := string(req.Header.Method())
		path := string(req.URI().PathOriginal())
		body := append([]byte(nil), req.Body()...) // own it past the request's reuse
		connClose := req.ConnectionClose()
		fasthttp.ReleaseRequest(req)

		// Process inline on this connection's worker. There's nothing to
		// parallelize across serial HTTP/1.1 requests; the App fans its own work
		// into sub-waves (visible to streampool), never blocking on an opaque
		// cross-task channel.
		resp := s.app(ctx, method, path, body)

		if err := writeH1(bw, &resp); err != nil {
			return
		}
		if err := bw.Flush(); err != nil || connClose {
			return
		}
	}
}

func writeH1(bw *bufio.Writer, r *Response) error {
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)
	resp.SetStatusCode(r.Status)
	if r.ContentType != "" {
		resp.Header.SetContentType(r.ContentType)
	}
	resp.SetBody(r.Body)
	return resp.Write(bw)
}
