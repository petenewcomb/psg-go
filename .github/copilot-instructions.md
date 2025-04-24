# PSG-Go Copilot Instructions

> **Important**: Check the TODO.md file in the repository root for current work-in-progress items and tasks that need attention.

## Project Overview
PSG-Go is a Go library that implements a pipelined scatter-gather concurrency pattern. It provides tools for managing concurrent tasks with controlled parallelism, backpressure, and result aggregation.

## Key Concepts
- **Scatter-Gather Pattern**: Tasks are scattered (launched) and their results gathered (processed) asynchronously
- **Backpressure**: Mechanisms to control resource usage by limiting concurrent task execution
- **Pool**: Controls concurrency limits for task execution
- **Job**: Manages the lifecycle of related tasks
- **Gather**: Processes results from completed tasks
- **Combiner**: Combines multiple inputs into a single output

## Architecture Guidelines
1. **Deadlock Prevention**: Any waiting mechanism should have an escape hatch. Be especially careful when:
   - Tasks need to wait for capacity
   - Results need to be gathered
   - The same entity is both launching tasks and processing results

2. **Backpressure Modes**:
   - `backpressureDecline`: Return without launching when capacity is full (for TryScatter)
   - `backpressureGather`: Block and gather results to make room (for Scatter)
   - `backpressureWaiter`: Wait for notification without gathering (for Combiner)

3. **Memory Control**: Always maintain proper constraints on memory usage. 
   - Limit the number of in-flight tasks
   - Ensure notification channels are properly sized and cleaned up

4. **Concurrency Safety**:
   - Ultrathink about concurrency issues, especially regarding potential orders of operations
   - Consider what happens when operations occur in unexpected sequences
   - Use atomic operations and proper synchronization

## Build/Test Commands
- Use `go vet ./...` to verify code correctness instead of running a build with `go build` unless you really need the executable(s)
- Run all tests in short mode for general functional validation: `go test -short ./...` as tests may take several minutes to run without `-short`
- Use `go test -run '^TestOrExampleName$' ./...` with or without `-short` to run a specific test or example
- Use `go test -coverprofile coverage.out -coverpkg ./...` with or without `-short` to calculate test coverage
- Remember that `go test` will usually output nothing upon success. To force it to generate output for all tests run use `-v`. Also pay attention to the exit code.
- Use `go test -race` to engage the race detector, which will slow execution time but detect at least egregious thread safety problems.
- For debugging command output that disappears quickly or is truncated, use `COMMAND 2>&1 | tee FILE` to capture both stdout and stderr to a file (e.g., `go test ./... 2>&1 | tee test-output.txt` or `.githooks/pre-commit 2>&1 | tee hook-output.txt`).

## Code Style
- See .githooks/pre-commit for expectations of code ready to review
- See .github/workflows/ci.yml and its dependencies for full build and test expectations
- See .github/workflows/release.yml and its dependencies for release expectations
- Update CHANGELOG.md according to the instructions and references in its preamble
- Expect and produce idiomatic Go code and documentation
- Diverge from well-known Go best practices only after thinking hard about alternatives and documenting your reasoning
- Ensure that all files include the same copyright header
- Ensure that each text file ends in a newline unless it's important that it does not  
- Organize the contents of files so that they read whole-to-part, top-to-bottom, as a narrative story insofar as possible
- Use `require` package for test assertions
- Handle errors explicitly - don't ignore them
- Use context properly for cancellation
- Document exported types and functions
- Document non-exported types and functions if there are non-evident details the reader should know
- Add explanatory and/or narrative comments to code when there are non-evident details the reader should keep in mind
- Do not add comments that effectively repeat what the code itself already says effectively
- Choose names that ensure that code is as self-documenting as possible
- Field and method naming should clearly indicate purpose
- Use meaningful types for enum-like constants
- Buffer channels when waiting for signals that might be sent before receivers are ready
- Focus not only on achieving intended functionality and behavior but also on making non-intended functionality and behavior impossible
- Validate user inputs explicitly if invalid inputs could cause delayed or difficult-to-understand errors, outputs, or behaviors 

## Testing
1. Test edge cases involving concurrency limits
2. Ensure deterministic behavior in tests
3. Test for deadlocks by running with minimal concurrency limits
4. Verify cancelation and cleanup work properly

## Common Pitfalls
1. **Circular Dependencies**: A task cannot gather its own results without deadlocking
2. **Missed Signals**: Unbuffered channels can miss signals if sent before receivers are listening
3. **Race Conditions**: Be careful when checking and modifying counters
4. **Resource Leaks**: Always clean up resources when contexts are canceled
