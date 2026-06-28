// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package edge

import (
	"bufio"
	"context"
	"net"

	"github.com/petenewcomb/streampool"
	"github.com/valyala/fasthttp"
)

// serveH1Pipelined drives one HTTP/1.1 connection with request pipelining: the
// reader dispatches each request as its own task and reads ahead without waiting
// for it to finish, so pipelined requests process concurrently. Responses are
// reassembled into request order by an ordered Funnel.
//
//   - process (a Launcher) runs the App for one request and submits the result,
//     tagged with its sequence number, to the ordered funnel.
//   - respFunnel ([streampool.NewFnOrderedFunnel]) invokes the write handler for
//     each response in request order, buffering out-of-order completions. The
//     constructor caps itself to a single serial instance, so the writer is
//     touched by one goroutine at a time.
//
// Backpressure bounds the reorder buffer: if the serial funnel falls behind,
// Submit paces the request tasks and, in turn, the reader — so a client cannot
// pipeline unboundedly and balloon the buffer.
func (s *Server) serveH1Pipelined(ctx context.Context, c net.Conn) {
	br := bufio.NewReader(c)
	bw := bufio.NewWriter(c)

	var rw streampool.Wave

	// Writes each response in request order as it becomes deliverable. Requests
	// are numbered from 0, so the resequencer starts at 0.
	resequencer := streampool.NewFnResequencer[Response](&rw, 0,
		func(_ context.Context, resp Response, _ error) error {
			if err := writeH1(bw, &resp); err != nil {
				return err
			}
			return bw.Flush()
		})

	process := streampool.NewLauncher[seqReq](
		streampool.HandlerFunc[seqReq](func(ctx context.Context, sr seqReq, _ error) error {
			resp := s.app(ctx, sr.method, sr.path, sr.body)
			return resequencer.Submit(ctx, sr.seq, resp)
		}),
	)

	var seq uint64
	for {
		req := fasthttp.AcquireRequest()
		if err := req.Read(br); err != nil {
			fasthttp.ReleaseRequest(req)
			break
		}
		sr := seqReq{
			seq:    seq,
			method: string(req.Header.Method()),
			path:   string(req.URI().PathOriginal()),
			body:   append([]byte(nil), req.Body()...),
		}
		connClose := req.ConnectionClose()
		fasthttp.ReleaseRequest(req)

		// Non-blocking dispatch (modulo backpressure): the reader keeps reading
		// pipelined requests while earlier ones are still processing.
		if err := process.In(&rw).Submit(ctx, sr); err != nil {
			break
		}
		seq++
		if connClose {
			break
		}
	}

	// Drain: finish all request tasks, deliver every response in order, flush.
	_ = rw.CloseAndSkimAll(ctx)
}

// seqReq is a request copied off the wire, tagged with its position so responses
// can be reordered.
type seqReq struct {
	seq    uint64
	method string
	path   string
	body   []byte
}
