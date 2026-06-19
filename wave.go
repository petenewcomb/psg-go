// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package psg

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/petenewcomb/psg-go/psgopt"
)

// Wave represents a batch of scatter-gather work tracked through the
// pipeline. Ops dispatch via the Wave-augmented context returned by
// [NewWave]; that context carries the Wave so every Start and Submit
// associates work with this batch for drain bookkeeping.
//
// In Wave 5a a Wave is bound 1:1 with an underlying [Pool] (the
// workers and queues that execute the work). A future wave relaxes
// this to allow multiple concurrent Waves on a shared Pool.
type Wave struct {
	pool *Pool
	// ownsPool is true when the Wave constructed its own Pool via
	// [NewWave] without a [WithPool] option. In that case the Wave's
	// drain methods also tear down the Pool. When the Pool was
	// supplied by the caller, lifecycle stays with the caller.
	ownsPool bool

	// wave-5b per-Wave substrate (being wired in; see
	// docs/global-substrate-activation.md). waveCtx = WithCancel(the global
	// defaultPool's poolCtx): per-wave cancellation and global pool teardown both
	// reach a running body by context ancestry. shells hands out the per-wave
	// borrowed execution contexts that global-pool workers run bodies under.
	// NOT YET on the execution path — task/funnel/skim still run on the legacy
	// per-Pool substrate until the producer cutover.
	waveCtx    context.Context //nolint:containedctx // per-wave cancellation root
	waveCancel context.CancelFunc
	shells     execShellPool

	// fEngine is this Wave's funnel machinery (the scheduled-flush queue and the
	// persistent flush driver). It is a deliberately lazy sub-object — nil until
	// the first NewFunnel(wave, …) — rather than fields flattened onto Wave, because
	// most waves never create a funnel and should not carry that state or spawn a
	// flusher. It is BATCH-scoped (per-Wave, not per-Pool): under WithPool each Wave
	// gets its own engine. Stored atomically so teardown (CancelAndWait) can read it
	// without racing a concurrent first creation; fEngineMu serializes the
	// create-once. (Funnel backpressure is NOT here — it registers on the wave's
	// shared governor; see funnelPostWork.Execute.)
	fEngineMu sync.Mutex
	fEngine   atomic.Pointer[funnelEngine]
}

// funnelEngine returns this Wave's lazily-created funnel engine, building it on
// the first call (double-checked under fEngineMu).
func (w *Wave) funnelEngine() *funnelEngine {
	if fe := w.fEngine.Load(); fe != nil {
		return fe
	}
	w.fEngineMu.Lock()
	defer w.fEngineMu.Unlock()
	if fe := w.fEngine.Load(); fe != nil {
		return fe
	}
	fe := newFunnelEngine(w.pool)
	w.fEngine.Store(fe)
	return fe
}

// WaveOption configures a Wave at construction time.
type WaveOption interface {
	applyToWaveConfig(*waveConfig)
}

type waveConfig struct {
	pool     *Pool
	poolOpts []psgopt.PoolOption
}

// WithPool binds the Wave to an existing [Pool] rather than letting
// the Wave construct one. The Pool's lifecycle stays with the caller
// — Wave methods on this Wave drain only this batch's work; the Pool
// itself is shut down by its own [Pool.CancelAndWait].
func WithPool(pool *Pool) WaveOption {
	return withPoolOption{pool: pool}
}

type withPoolOption struct {
	pool *Pool
}

func (o withPoolOption) applyToWaveConfig(c *waveConfig) {
	c.pool = o.pool
}

// WithPoolOptions forwards Pool-construction options to the Pool the
// Wave creates internally. Has no effect when [WithPool] is also
// supplied (the caller-provided Pool is used as-is).
func WithPoolOptions(opts ...psgopt.PoolOption) WaveOption {
	return withPoolOptionsOption{opts: opts}
}

type withPoolOptionsOption struct {
	opts []psgopt.PoolOption
}

func (o withPoolOptionsOption) applyToWaveConfig(c *waveConfig) {
	c.poolOpts = append(c.poolOpts, o.opts...)
}

// NewWave constructs a Wave and returns it together with a
// Wave-augmented context callers should pass to op dispatches
// (Start, Submit). Without [WithPool] the Wave creates a fresh Pool
// using parent as the root context and tears it down on
// [Wave.CancelAndWait]; with [WithPool] the supplied Pool is reused
// and its lifecycle stays with the caller.
func NewWave(parent context.Context, opts ...WaveOption) (context.Context, *Wave) {
	var cfg waveConfig
	for _, opt := range opts {
		opt.applyToWaveConfig(&cfg)
	}

	var (
		pool     *Pool
		ownsPool bool
	)
	if cfg.pool != nil {
		pool = cfg.pool
	} else {
		pool = New(parent, cfg.poolOpts...)
		ownsPool = true
	}

	w := &Wave{pool: pool, ownsPool: ownsPool}

	// wave-5b: derive the per-wave cancellation root from the global pool's
	// teardown context. The execShell pool is Init'd below, once the top-level meta
	// gives us the wave's job ancestry (parentJobs).
	w.waveCtx, w.waveCancel = context.WithCancel(defaultPool.PoolCtx())

	// Inject the Wave into the ctxMeta of the returned ctx so op
	// dispatches can find it. Use topLevelCtxMeta so the cached meta
	// also has its executionEnvironment populated — otherwise
	// subsequent topLevelCtxMeta calls hit the cache without ever
	// running the updateFn that would set exEnv.
	ctx, meta := pool.topLevelCtxMeta(parent, func(ctxType contextType) {
		if ctxType != topLevelContext {
			panic(fmt.Sprintf(
				"NewWave called from %v context but allowed only by top-level context",
				ctxType))
		}
	})
	meta.wave = w

	// Build the per-wave execShell pool, stamping the wave's job ancestry
	// (meta.parentJobs, computed by ensureCtxMeta when this wave descends from a
	// body in another job) onto every shell so the cross-job guards fire. Not yet
	// on the execution path for funnel/skim (legacy per-Pool substrate runs those);
	// task bodies run under these shells.
	w.shells.Init(w.waveCtx, pool, w, meta.parentJobs)

	return ctx, w
}

// Pool returns the [Pool] this Wave is bound to. Exposed for advanced
// uses such as binding another op to the same Pool; typical callers
// don't need this.
func (w *Wave) Pool() *Pool {
	return w.pool
}

// resolveWave returns the op's bound wave if non-nil, otherwise
// looks up the wave attached to ctx by [NewWave]. Panics if neither
// is set — an op constructed with nil wave must be dispatched from a
// ctx that descends from a NewWave call.
//
// This is the dispatch-side counterpart to nil-OK construction:
// constructing with a specific *Wave locks dispatch to that wave;
// constructing with nil defers the choice to the dispatching ctx,
// letting one op instance be reused across many waves.
func resolveWave(opWave *Wave, ctx context.Context) *Wave {
	if opWave != nil {
		return opWave
	}
	meta, ok := ctx.Value(ctxMetaValueKey{}).(*ctxMeta)
	if !ok || meta.wave == nil {
		panic("op constructed with nil wave dispatched from a ctx with no wave (call NewWave first)")
	}
	return meta.wave
}

// Cancel signals all work tagged with this Wave to terminate. When
// the Wave owns its Pool (constructed via [NewWave] without
// [WithPool]), this cancels the Pool's root context too.
func (w *Wave) Cancel() {
	// wave-5b: cancel the per-wave context. Once work runs under borrowed shells
	// (descendants of waveCtx) this is what reaches a running body; today it is
	// additive and the Pool cancel below still does the real teardown.
	w.waveCancel()
	if w.ownsPool {
		w.pool.Cancel()
		return
	}
	// TODO(wave-5b): with shared Pool, cancel only this Wave's work
	// via a derived ctx. Until then the per-Wave cancel signal is the
	// Pool's own cancel.
	w.pool.Cancel()
}

// CancelAndWait cancels this Wave's work then waits for it to drain.
// When the Wave owns its Pool, the Pool's workers also exit before
// this call returns.
func (w *Wave) CancelAndWait() {
	w.waveCancel()
	// wave-5b: release the per-wave execShells. Harmless today (no body has
	// borrowed one); once workers run bodies under shells this reaps their
	// contexts promptly rather than waiting for waveCtx GC.
	defer w.shells.release()
	// Cancel the pool context first to drive this wave's funnel flusher toward
	// exit, then JOIN it before the pool teardown below clears the ctxMetaMaps —
	// the flusher reads those maps (ensureCtxMeta, flush bodies), so clearing them
	// while it still runs is a data race. The flusher is wavestate-joined, not
	// Pool.wg-tracked (see funnelEngine.flusherDone). Cancel is idempotent, so the
	// CancelAndWait below repeating it is harmless.
	w.pool.Cancel()
	if fe := w.fEngine.Load(); fe != nil {
		fe.joinFlusher()
	}
	// TODO(wave-5b): with a shared Pool (WithPool) this cancels the whole Pool;
	// it should drain only this Wave's tagged work.
	w.pool.CancelAndWait()
}

// Close signals that no more new top-level dispatches will be made
// to this Wave. Existing in-flight work continues; use
// [Wave.SkimAll] or [Wave.CloseAndSkimAll] to wait for it.
func (w *Wave) Close() {
	w.pool.Close()
}

// Skim pumps the Wave's queue once: it dispatches at least one
// completed Skimmer's result through its handler, blocking until
// either a handler runs or the context is canceled.
func (w *Wave) Skim(ctx context.Context) error {
	return w.pool.Skim(ctx)
}

// TrySkim attempts to pump the Wave's queue without blocking.
// Returns (true, nil) if a handler ran, (false, nil) if there was
// nothing ready, or (false, non-nil) on error.
func (w *Wave) TrySkim(ctx context.Context) (bool, error) {
	return w.pool.TrySkim(ctx)
}

// SkimAll pumps the Wave's queue until every in-flight item has
// completed.
func (w *Wave) SkimAll(ctx context.Context) error {
	return w.pool.SkimAll(ctx)
}

// TrySkimAll pumps the Wave's queue until it is empty, but never
// blocks waiting for more work.
func (w *Wave) TrySkimAll(ctx context.Context) error {
	return w.pool.TrySkimAll(ctx)
}

// CloseAndSkimAll signals no more top-level dispatches will be
// made, then drains the queue until every in-flight item has
// completed.
func (w *Wave) CloseAndSkimAll(ctx context.Context) error {
	return w.pool.CloseAndSkimAll(ctx)
}
