# Original Layered RDVQ Implementation

This folder contains the original 4-layer RDVQ implementation that showed ~23% performance regression compared to the baseline.

## Architecture

The original implementation used a 4-layer design:

```
Patient[T] → Tolerant[T] → Strict[T] → baseQ[T]
```

## Performance Investigation Results

- **Baseline**: 94 RDVQ function calls in assembly
- **Layered**: 313 RDVQ function calls in assembly (3.3x increase)
- **Performance impact**: ~23% throughput regression
- **Root cause**: Call chain depth prevents effective inlining despite individual functions being "inlinable"

## Key Findings

1. **Inlining Paradox**: Individual functions show as "can inline" but actual assembly contains CALL instructions
2. **Call Chain Explosion**: Each layer adds function calls that compound through the hot path
3. **Generic Complexity**: Complex generic instantiations exhaust Go's inlining budget
4. **Closure Overhead**: Function pointer arguments create calling conventions that prevent optimization

## Optimization Attempt

The next step is to remove the Tolerant layer since:
- No direct usage of `rdvq.Tolerant` in the codebase
- Patient can potentially call Strict directly
- This would reduce call chain depth from 4 to 3 layers

Target architecture:
```
Patient[T] → Strict[T] → baseQ[T]
```

This represents a ~25% reduction in call chain depth which may restore inlining effectiveness.