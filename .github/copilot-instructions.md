# Copilot Instructions

> **Important**: Check the TODO.md file in the repository root for current work-in-progress items and tasks that need attention.

> **Meta-instruction**: If you notice the user repeatedly correcting or advising on the same type of issue, proactively suggest adding guidance to these instructions to prevent future repetition.

## Reference materials
See `README.md` for a project overview, the `docs` folder for design documentation, and the `benchmarks` for previous investigations.

## Concurrency Safety
- Ultrathink about concurrency issues, especially regarding potential orders of operations
- Consider what happens when operations occur in unexpected sequences
- Use atomic operations when possible in hot paths, but ensure proper synchronization everywhere 

## Build/Test/Source Control Commands
- Use `goimports` to fix up imports
- Use `go vet ./...` to verify code correctness instead of running a build with `go build` unless you really need the executable(s)
- Run all tests in short mode for general functional validation: `go test -short ./...` as tests may take several minutes to run without `-short`
- Use `go test -run '^TestOrExampleName$' ./...` with or without `-short` to run a specific test or example
- Use `go test -coverprofile coverage.out -coverpkg ./...` with or without `-short` to calculate test coverage
- Remember that `go test` will usually output nothing upon success. To force it to generate output for all tests run use `-v`. Also pay attention to the exit code.
- Use `go test -race` to engage the race detector, which will slow execution time but detect at least egregious cross-thread data access problems.
- Avoid adding unrelated untracked files to a commit.  Prefer `git add -u` over `git add .`, or better yet just stage files by naming them explicitly.
- Use `.githooks/pre-commit` to run pre-commit checks before attempting a commit; pay attention to its return code and realize that it may make modifications that mean files must be (re-)staged.
- When running benchmarks, always set bash timeout greater than the expected duration to account for overhead (including warmup)

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
- Use `require` package for test assertions, usually by instantiating a `chk` variable with `require.New`
- Handle errors explicitly - don't ignore them
- Use the context package properly, especially to enable cancellation where appropriate
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

## Documentation Style
- Design documents should be mostly prose but still include key diagrams, small code blocks, and structured lists where they help with clarity or pedagogy.
- Make sure that any tunable parameters are referred to as such and avoid implying that any concrete values provided as examples are dictated by the design.
- Always include discussion of rejected alternatives.

## Testing
1. Test edge cases involving concurrency limits
2. Ensure deterministic behavior in tests
3. Test for deadlocks by running with minimal concurrency limits
4. Verify cancellation and cleanup work properly
5. Use `pgregory.net/rapid` to create robust property tests
