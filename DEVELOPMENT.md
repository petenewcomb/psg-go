# Development Guide

This document provides guidance for anyone (human or AI) working on the PSG-Go codebase.

## Reference Materials
- See `README.md` for project overview, `docs` folder for design documentation, `benchmarks` for previous investigations
- Check `TODO.md` for branch-specific work requirements and pre-merge checklists
- Review `WORKING_NOTES.md` for current development context and implementation insights needed for remaining work
- Review GitHub issues for project-wide planning and cross-branch work streams

## Concurrency Safety
- Think deeply and systematically about concurrency issues, especially regarding potential orders of operations
- Consider what happens when operations occur in unexpected sequences
- Use atomic operations when possible in hot paths, but ensure proper synchronization everywhere
- Pay special attention to lifecycle management during shutdown phases - ensure all components complete their work properly
- Ensure reference counting and resource accounting is watertight to prevent deadlocks during cleanup 

## Build/Test/Source Control Commands

### Code Quality & Validation
- Use `goimports` to fix up imports
- Use `go vet ./...` to verify code correctness instead of running a build with `go build` unless you really need the executable(s)
- Use `.githooks/pre-commit` to run pre-commit checks before attempting a commit; pay attention to its return code and realize that it may make modifications that mean files must be (re-)staged.

### Testing
- Run all tests in short mode for general functional validation: `go test -short ./...` as tests may take several minutes to run without `-short`
- Use `go test -run '^TestOrExampleName$' ./...` with or without `-short` to run a specific test or example
- Use `go test -coverprofile coverage.out -coverpkg ./...` with or without `-short` to calculate test coverage
- Use `go test -race` to engage the race detector, which will slow execution time but detect at least egregious cross-thread data access problems.
- Remember that `go test` will usually output nothing upon success. To force it to generate output for all tests run use `-v`. Also pay attention to the exit code.

### Benchmarking
- When running benchmarks, always set bash timeout greater than the expected duration to account for overhead (including warmup)

### Git & Source Control
- Avoid adding unrelated untracked files to a commit.  Prefer `git add -u` over `git add .`, or better yet just stage files by naming them explicitly.
- Always verify git commit success by checking the command output. Pre-commit hooks can fail silently or make formatting changes that prevent the commit. Look for error output, and if the pre-commit hook runs `git status --porcelain -uno | grep '^[^ ][^ ]'` and finds modified files, the commit failed. You must stage any hook-generated changes and retry the commit.

## Code Style

### Process & Tooling
- See .githooks/pre-commit for expectations of code ready to review
- See .github/workflows/ci.yml and its dependencies for full build and test expectations
- See .github/workflows/release.yml and its dependencies for release expectations
- Update CHANGELOG.md according to the instructions and references in its preamble

### Code Quality & Go Best Practices
- Expect and produce idiomatic Go code and documentation
- Diverge from well-known Go best practices only after thinking hard about alternatives and documenting your reasoning
- Handle errors explicitly - don't ignore them
- Use the context package properly, especially to enable cancellation where appropriate

### Formatting & File Organization
- Ensure that all files include the same copyright header
- Ensure that each text file ends in a newline unless it's important that it does not  
- Organize the contents of files so that they read whole-to-part, top-to-bottom, as a narrative story insofar as possible

### Naming & Documentation
- Choose names that ensure that code is as self-documenting as possible
- Field and method naming should clearly indicate purpose
- Use meaningful types for enum-like constants
- Document exported types and functions
- Document non-exported types and functions if there are non-evident details the reader should know
- Add explanatory and/or narrative comments to code when there are non-evident details the reader should keep in mind
- Do not add comments that effectively repeat what the code itself already says effectively

### Design & Safety
- Focus not only on achieving intended functionality and behavior but also on making non-intended functionality and behavior impossible
- Validate user inputs explicitly if invalid inputs could cause delayed or difficult-to-understand errors, outputs, or behaviors
- Buffer channels when waiting for signals that might be sent before receivers are ready

### Testing Standards
- Use `require` package for test assertions, usually by instantiating a `chk` variable with `require.New` 

## Documentation Style
- Documentation is focused on teaching concepts and details that readers need to understand to use, modify, or extend the code easily and effectively.
- Documentation is not for highlighting accomplishments and must not make unsubstantiated claims.
- Benchmark results and other empirical evidence should be used to support or refute that a design or implementation solves a stated problem.
- Design documents should be mostly prose but still include key diagrams, small code blocks, and structured lists where they help with clarity or pedagogy.
- Make sure that any tunable parameters are referred to as such and avoid implying that any concrete values provided as examples are dictated by the design.
- Always include discussion of rejected alternatives.
- Tell the story, don't list features: Design docs should narrate why a component exists, what problem it solves, and how it fits into the broader architecture. Avoid feature lists or API documentation—focus on the conceptual story.
- Ground documentation in actual code and constraints: Base explanations on what actually exists in the codebase and the real constraints that shaped the design. Don't invent hypothetical alternatives unless they were genuinely considered. Refer to specific files, functions, and code patterns when explaining how something works.
- Respect architectural uniqueness: PSG doesn't follow standard patterns (like traditional queuing systems). Design docs should explain how PSG's unique approach (notification conservation, structured concurrency constraints, etc.) shapes the solutions. Don't force conventional explanations onto unconventional architectures.
- Connect to broader principles: Show how individual components serve PSG's fundamental goals (deadlock prevention, notification conservation, performance). Explain how architectural constraints enable the solutions rather than limit them.
- Focus on integration patterns: Emphasize how components integrate with existing PSG infrastructure rather than standing alone. Show how new solutions leverage existing patterns (like Waiters, context hierarchy, atomic coordination) rather than inventing new coordination mechanisms.

## Tracing Guidelines

PSG-Go uses comprehensive tracing instrumentation controlled by the `PSGTRACEINTERNALS` environment variable. Follow these principles when adding or maintaining tracing code:

### Essential Tracing Patterns to Include
- **Select branch logging**: Always log which branch of a select statement was taken, as this cannot be easily inferred and is crucial for debugging concurrent behavior
- **Channel pointer correlation**: Log when channels are actually used (not just when they participate in a select) - the pointer values help correlate channel usage across goroutines  
- **Async workflow correlation**: Use `workID` logging to correlate related work across scatter/gather lifecycles and async boundaries
- **Cross-system coordination**: Log handoffs between different subsystems (task pools, combiner pools, etc.)
- **Component relationship tracking**: In New/Init functions, log pointers to the created object and its key internal components to establish debugging relationships
- **Object method tracking**: From tracked methods, log the pointer to the object being operated on to maintain object correlation throughout its lifecycle

### Tracing Patterns to Avoid
- **Method entry/exit redundancy**: Avoid "calling X" / "called X" logs when the called method handles its own tracing appropriately
- **State change echoing**: Avoid logs that merely echo state changes already logged by the underlying operations
- **StartTask usage**: Avoid `trace.StartTask` due to context-creation overhead - use `workID` correlation instead for tracking async work
- **Context propagation for tracing**: Avoid propagating contexts solely for tracing purposes

### Standard Tracing Structure
Use this consistent pattern throughout the codebase:
```go
func NewComponent() *Component {
    traceRegion := "NewComponent"
    defer trace.StartRegion(ctx, traceRegion).End()
    c := &Component{...}
    trace.Logf(ctx, traceRegion, "Component=%p, internalQueue=%p, state=%p", 
        c, &c.internalQueue, &c.state)
    return c
}

func (c *Component) MyMethod() {
    traceRegion := "Component.MyMethod"
    defer trace.StartRegion(ctx, traceRegion).End()
    trace.Logf(ctx, traceRegion, "Component=%p", c)
    
    // For nested functions/closures:
    innerFn := func() {
        traceRegion := traceRegion + ".innerFn" 
        defer trace.StartRegion(ctx, traceRegion).End()
        // ...
    }
}
```

### Context Usage in Tracing
- Use `context.Background()` for tracing when a context is not available - tracing should not require context propagation
- Prefer passed contexts when available, but don't propagate contexts solely for tracing purposes

### When to Use StartRegion
- Use `StartRegion` for significant logical boundaries and entry points
- Avoid `StartRegion` for simple helper functions called in predictable patterns where the calling context already provides sufficient tracing coverage

## Testing Guidelines
1. Test edge cases involving concurrency limits
2. Ensure deterministic behavior in tests
3. Test for deadlocks by running with minimal concurrency limits (especially low values like 1-8)
4. Verify cancellation and cleanup work properly
5. Use `pgregory.net/rapid` to create robust property tests
6. When testing performance changes, use consistent benchmark environments and compare normalized results
7. Test that pools and goroutine management scale down to zero when idle
