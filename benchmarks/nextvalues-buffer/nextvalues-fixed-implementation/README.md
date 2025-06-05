# Fixed nextValues Implementation

This folder contains the corrected nextValues RDVQ implementation that fixes the abandonment bugs discovered during investigation.

## Fixes Applied

### 1. Abandonment Bug Fix (rdvq.go line 136)
**Problem**: When receivers timed out, the cleanup logic had conditional logic that could lose values:
```go
// BUGGY CODE:
if result.OK {
    q.nextValues.PushBack(&p.valuePool, drainedValue)
} else {
    result.Value = drainedValue  // BUG: Trying to change the past
    result.OK = true            // BUG: BlockFunc already decided
}
```

**Fix**: Always preserve drained values for the next receiver:
```go
// FIXED CODE:
drainedValue := <-receiverCh
q.nextValues.PushBack(&p.valuePool, drainedValue)
```

**Root Cause**: Once a `BlockFunc` times out and returns `BlockResult{OK: false}`, it has already captured state (like `ctx.Err()`) and made its decision. You cannot retroactively change that decision.

### 2. TryPopFront Bug Fix (rdvq.go lines 188-199)
**Problem**: `TryPopFront` couldn't access values stuck in abandoned receiver channels:
```go
// BUGGY CODE:
func (q *Queue[T]) TryPopFront(p *Pool[T]) (T, bool) {
    if value, ok := q.nextValues.PopFront(&p.valuePool); ok {
        return value, true
    }
    select {
    case value := <-q.sharedChan:
        return value, true
    default:
        return *new(T), false
    }
}
```

**Fix**: Use `PopFrontFunc` to benefit from abandonment cleanup:
```go
// FIXED CODE:
func (q *Queue[T]) TryPopFront(p *Pool[T]) (T, bool) {
    return q.PopFrontFunc(p, func(dedicatedCh, sharedCh <-chan T) BlockResult[T] {
        select {
        case value := <-dedicatedCh:
            return NewBlockResult(value, true, dedicatedCh)
        case value := <-sharedCh:
            return NewBlockResult(value, true, sharedCh)
        default:
        }
        return NewBlockResult(*new(T), false, nil)
    })
}
```

### 3. Enhanced Test Logging (rdvq_test.go lines 337-343)
Added detailed conservation checking to help debug value loss issues.

## Test Results

With these fixes applied, the stress test with abandonments passes with perfect value conservation:

```
=== RUN   TestQueue_StressWithAbandonments
    rdvq_test.go:325: Pushed: 1578286, Popped: 1578279, PushFailures: 2, PopAttempts: 992076
    rdvq_test.go:339: Conservation check: successful_pushes=1578284, consumed=1578279, remaining=5
--- PASS: TestQueue_StressWithAbandonments (2.00s)
```

Perfect conservation: 1,578,284 = 1,578,279 + 5 ✅

## Benchmark Results

While the fixes restore correctness, they don't improve performance over simpler approaches:

- **Fixed nextValues**: ~3,200 tasks/sec, ~66 allocs/op, ~2,100 B/op
- **Pre-RDVQ (channel+queue)**: ~3,400 tasks/sec, ~48 allocs/op, ~1,680 B/op

The simpler pre-RDVQ approach is 6-11% faster with fewer allocations.

## Conclusion

The fixes successfully resolve the data loss bugs, but the nextValues buffer approach adds complexity without performance benefits compared to simpler callback-based approaches.