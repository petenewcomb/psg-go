# streamhttp / streamgrpc: pairing streampool with an HTTP edge

This is a design summary for the `streamhttp` and `streamgrpc` modules — the
example front-ends that pair streampool with an HTTP-family server (HTTP/1.1,
HTTP/2, WebSocket, gRPC). They are illustrative demonstrations, not products; the
one reusable primitive they surfaced, [`Resequencer`](../../resequencer.go),
lives in the core package. This document records the decisions and, per the
project's documentation style, the alternatives that were rejected and why.

The single idea that organizes everything below: **streampool is the work
scheduler, and the HTTP layer's only job is to accept connections, parse
requests, and submit them.** Every decision falls out of matching the transport
to that division of labor.

## The front end: net/http, goroutine-per-connection

`streamhttp` is a standard `net/http` server. One `http.Server` over a TLS
listener serves HTTP/1.1 and HTTP/2 by ALPN automatically; handlers run on
net/http's per-request (H/1) or per-stream (H/2) goroutine and fan work out to
streampool via sub-waves. See `streamhttp/server.go`.

This is the harmonious pairing because of streampool's own nature. Its pool is
unbounded and *blocking-first-class*: a worker that blocks on I/O is exactly what
the pool is built to absorb (see
[backpressure-and-reentrancy](backpressure-and-reentrancy.md)). Goroutine-per-
connection is therefore not overhead to design around — it is the natural fit,
and the Go runtime's netpoller is already the event loop that makes it cheap. As
a bonus, net/http hands every request an `r.Context()` that cancels on client
disconnect, so cancellation propagates into streampool by context ancestry for
free.

**Rejected — a userspace event loop (gnet and similar).** An event loop
reimplements, in userspace, the epoll the Go runtime already provides, and it
*fights* streampool: its model is non-blocking callbacks, streampool's is freely
blocking adaptive workers, and it brings its own goroutine pool — three
schedulers stacked where one would do. The `breeze` project is the cautionary
example: an event-loop HTTP server with a hand-rolled worker pool and
concurrency bugs. The event loop only earns its place at extreme idle-connection
counts, which is a WebSocket concern (below), not an HTTP-request one.

**Rejected — the fasthttp codec.** fasthttp's sole advantage over net/http is
per-request allocation. Measured on identical request parsing:

| codec | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| net/http | ~2100 | ~1060 | 11 |
| fasthttp | ~1200 | 0 | 0 |

Real and large in isolation — decisive for a trivial, high-rate edge (proxy,
gateway, static) where per-request GC pressure dominates. But irrelevant to a
streampool-shaped edge: once a handler does real work, the ~1 KB / 11 allocs / ~1
µs of net/http parsing is dwarfed by the handler itself (a single `json.Marshal`
of a small response is comparable to the entire parse). Worse, fasthttp's types
are not `net/http`, so it is the *only* piece that doesn't compose — it forces a
separate front-end for WebSocket (which needs a net/http upgrade), for HTTP/2
(x/net/http2 over a `net.Conn`), and for gRPC (`ServeHTTP`). net/http unifies all
four on one listener; fasthttp buys allocations we have to spare in exchange for
fragmenting the stack.

## Admission is not a limiter

`streamhttp` has no admission-concurrency knob. In-flight work is bounded
naturally — by the connection count (one request in flight per HTTP/1.1
connection), by HTTP/2's `MaxConcurrentStreams`, and by streampool's end-to-end
backpressure pacing top-level submission when a downstream stage is saturated.

A `Limiter` models a *real external constraint* — "this backend allows ≤64
concurrent calls," "≤100 req/s" — not request admission. Putting a semaphore on
the request gate is an arbitrary number that second-guesses the framework's own
pacing. `streamhttp/fanout.go` and `streamgrpc` demonstrate the legitimate use: a
shared `Limiter` bound to the downstream fan-out, capping one dependency
collectively across every caller regardless of transport.

The one structural exception to "limiters model external constraints" is a
concurrency-1 limiter used to *serialize a reducer* — see the Resequencer below.

## WebSocket

**Default: gws, goroutine-per-connection.** A WebSocket connection is just a
connection that kept reading after the upgrade, so `streamhttp` handles it on the
same standard-TLS, goroutine-per-connection model as everything else — no event
loop, no forked TLS. On each message, `OnMessage` transfers gws's pooled
`*Message` to streampool *without copying* (gws hands us buffer ownership; the
worker `Close()`s it after processing, recycling to gws's pool) and returns
immediately, keeping the read goroutine's parked stack shallow. Replies are
written with `WriteAsync`, which serializes per connection so concurrent workers
cannot interleave frames.

**Ordered replies are per-message-selectable.** Because WebSocket has no
wire-level request/reply coupling — unlike HTTP/1.1 pipelining, which forces
*total* response ordering — ordering is an application choice. `WithOrderedWS`
takes a classifier; messages it marks are tagged with a sequence number on
arrival (serial per connection) and their replies are delivered in order through
a per-connection [`Resequencer`](../../resequencer.go), while everything else is
written as soon as it finishes. Ordered and unordered replies coexist on one
socket; only the ordered subset consumes sequence numbers, so the resequencer
always sees a gap-free run. This is the scatter-process-gather-in-order the
retired HTTP/1.1 pipelining example demonstrated, now on a live transport and
selectable per message.

**The event-driven variant is a different quadrant, chosen by TLS termination.**
At extreme counts of mostly-idle connections — the canonical large-scale
WebSocket workload — goroutine-per-connection's per-connection stack becomes a
real memory cost, and an event-driven poller (no goroutine per idle connection)
wins. Whether that is even reachable is decided by one question, **where TLS
terminates**, because TLS termination and the I/O model are the same axis:

- **In-process TLS ⇒ goroutine-per-connection ⇒ gws.** Standard `crypto/tls` is
  blocking and pull-based — it *owns* its read and blocks when it needs more
  bytes. That composes only with a stackful coroutine that can suspend on the
  blocking read, and a Go goroutine *is* exactly that (which is why blocking TLS
  "just works" in this model, for free).
- **Cleartext internal / TLS at the edge LB ⇒ event-driven ⇒
  cloudwego/netpoll + gobwas/ws.** An epoll loop is push/readiness-based and
  cannot drive blocking pull-TLS, so the event-driven path only works when there
  is no TLS in the loop at all.
- **nbio is the outlier.** It does in-process *async* TLS via a fork of
  `crypto/tls` — the one combination that pays a maintenance/security cost (a
  forked TLS stack that must track upstream CVEs) precisely in the quadrant where
  the memory win is already eroded: per-connection cipher state (1–4 KB) outweighs
  the ~2 KB goroutine stack the event loop saves. The headline "a million
  connections in a few GB" is fundamentally a *cleartext* number.

So the diagonal is the sane choice — in-process TLS with gws, or edge-terminated
cleartext with an event loop — and `streamhttp` takes the former, consistent with
the rest of the edge.

## The Resequencer

`Resequencer[T]` (and `RangeResequencer[T]`, its variable-width `[offset,
offset+length)` generalization) packages scatter-process-gather-in-order: fan
work out tagged with monotonic sequence numbers, let it complete in any order,
and have a single handler observe the results in order. Sequence numbers form a
gap-free run from a configurable start.

A resequencer is a *reducer* — a serial fold over shared next-sequence state — so
it must run as a single instance. That is the one place a concurrency-1 limiter
is correct: a [`Funnel`](../../funnel.go) is parallel by default (the framework
may run several accumulator instances at once), and without the cap a stateful
ordering fold races or stalls. `NewResequencer` binds `WithLimits(NewSemaphore(1))`
so the guarantee cannot be dropped by a caller. The Resequencer is justified and
tested by its own core tests and `Example_resequencer`; `streamhttp`'s ordered-WS
path is its real-world showcase.

## gRPC

grpc-go owns its HTTP/2 transport — framing, HPACK, flow control — and it is not
hand-rolled. `streamhttp` mounts a `grpc.Server` on the same listener by
content-type dispatch (`grpc.Server.ServeHTTP` on the H/2 path), so gRPC, HTTP,
and WebSocket share one port. This is the convenient single-port path; for
production, grpc-go's own `Serve(listener)` on a dedicated port is the better-
tested option, and either way the method bodies fan out via streampool — see
`streamgrpc`, whose `Aggregate` method fans out under a shared `Limiter`, the same
`Limiter` type a `streamhttp` handler would bind.

## When not to use this shape

- **Millions of mostly-idle connections** (a large WebSocket fleet): terminate
  TLS at the edge LB and use the event-driven variant (cloudwego/netpoll +
  gobwas/ws) speaking cleartext internally.
- **A high-rate, near-trivial edge** (proxy, gateway, echo): fasthttp's
  allocation win flips to decisive; keep it and accept the fragmentation.
- Everything in between — a front end whose handlers do real work and fan out —
  is what `streamhttp` is for, and the standard library is the right substrate.
