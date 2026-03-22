# Repository Instructions

- For Go tests in `/Users/sunjinlee/workspace/wod-strategist/api`, use `Ginkgo` and `Gomega` instead of plain `testing`-style test cases.
- When adding tests to a new Go package in `api`, add a `*_suite_test.go` file with `RunSpecs(...)`.
- For outbound HTTP unit tests in `api`, prefer `api/internal/testhelpers.MockTransport` to mock requests at the transport layer before falling back to ad-hoc servers.
- Standard Go benchmarks such as `Benchmark...` functions can stay in the normal `testing` package style.
- In `api`, required runtime environment variables must be validated in `internal/config` during startup initialization, not lazily inside request handlers or workers.
- In `api`, packages under `internal/` should return `error` values instead of calling `panic` or terminating the process directly. Process termination belongs in `cmd/server` and `cmd/worker`.
