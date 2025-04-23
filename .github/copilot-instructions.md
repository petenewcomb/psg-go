## Build/Test Commands
- Use `go vet ./...` to verify code correctness instead of running a build with `go build` unless you really need the executable(s)
- Run all tests in short mode for general functional validation: `go test -short ./...` as tests may take several minutes to run without `-short`
- Use `go test -run '^TestOrExampleName$' ./...` with or without `-short` to run a specific test or example
- Use `go test -coverprofile coverage.out -coverpkg ./...` with or without `-short` to calculate test coverage
- Remember that `go test` will usually output nothing upon success.  To force it to generate output for all tests run use `-v`.  Also pay attention to the exit code.
- Use `go test -race` to engage the race detector, which will slow execution time but detect at least egregious thread safety problems.

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
- Focus not only on achieving intended functionality and behavior but also on making non-intended functionality and behavior impossible
- Validate user inputs explicitly if invalid inputs could cause delayed or difficult-to-understand errors, outputs, or behaviors 
- Ultrathink about concurrency issues, especially regarding potential orders of operations
