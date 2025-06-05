# Pre-nextValues Working Implementation

This folder contains the RDVQ implementation from commit bed6a6d that **does NOT have the abandonment bug**.

## Key Characteristics

- **No `nextValues` buffer** - uses immediate callback processing
- **Perfect value conservation** - stress test passes with 0 lost values
- **Callback-based API** - `PopFront(ctx, pool, processFn)`
- **Immediate abandonment handling** - calls `processFn(ctx, drainedValue)` directly

## Test Results

```
=== RUN   TestQueue_StressWithAbandonments
    rdvq_test.go:341: Pushed: 1754717, Popped: 1754717, PushFailures: 0, PopAttempts: 1123436
--- PASS: TestQueue_StressWithAbandonments (2.00s)
```

**Perfect conservation**: 1,754,717 pushed = 1,754,717 popped + 0 remaining

## Abandonment Logic

When a receiver abandons its channel, the cleanup immediately processes any drained values:

```go
default:
    // Channel is full, drain the pending work and requeue to be
    // picked up by the next pop operation.
    drainedValue := <-receiverCh
    processFn(ctx, drainedValue)  // ← IMMEDIATE PROCESSING
```

This proves that the `nextValues` buffer refactor introduced the abandonment bug that affects all subsequent versions.