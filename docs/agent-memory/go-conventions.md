# Go Coding Conventions

## Interfaces

- **Do not create interfaces unless they will have multiple implementations.**
  If only one struct implements it, use the concrete type directly.
  Go interfaces exist for polymorphism, not for mocking.
- **Accept interfaces only at the consumer boundary** (e.g. `io.Reader`), not
  at the definition site. Follow the Go proverb: "Accept interfaces, return
  structs" — but only when an interface already exists or is genuinely needed.
- **Never create an interface solely for testing.** If you need to test code
  that depends on a concrete type, pass the real type with nil or stub
  dependencies. See [backend-testing.md](backend-testing.md) for the
  real-client testing strategy.
- **Example of what NOT to do:**
  ```go
  // ❌ Bad — interface mirrors a single struct 1:1, exists only for mock-seam
  type Handlers interface {
      Health(*gin.Context)
      GetHistory(*gin.Context)
      // ... 30+ methods
  }
  ```
  ```go
  // ✅ Good — use the concrete type directly
  func SetupRouter(ctl *controllers.Controller) { ... }
  ```

## Testing

- Prefer integration-style tests that exercise multiple layers (middleware →
  handler → DB) over isolated unit tests with mocks.
- Use real dependencies with minimal config for route-level tests. Test helpers
  must supply required constructor dependencies, such as `StorageClient`, even
  when the route under test does not use them.
- When the real handler returns 400 (missing body) instead of 401 (middleware
  blocked), that proves the request reached the handler — no mock needed.
- See [backend-testing.md](backend-testing.md) for worker-specific patterns.

## Function Signatures

- Accept concrete types unless you genuinely need polymorphism.
- When adding a new handler, register it directly in `SetupRouter`
  (`api.GET("/path", ctl.Method)`) — no route-definition tables, no
  interface methods to declare.

## Initialization errors

- Constructors in reusable packages return errors for missing or invalid
  dependencies; they do not call `panic`, `log.Fatal`, or `os.Exit`.
- Process termination decisions belong in `cmd/server` and `cmd/worker`, after
  constructor errors have propagated to the command boundary.

## Handler organization

- Split handler files by domain. `handlers.go` is a legacy catch-all
  (~1500 lines) and should not grow. New endpoints go in their own file
  (`session_handlers.go`, `upload_handlers.go`, etc.).
- When touching an existing handler in `handlers.go`, pull it into a
  domain-named file as part of the change.
- Methods on `*Controller` stay in `controllers/`; route registration
  stays in `server.SetupRouter`.

## Data access — no repository pattern

- Handlers call `ctl.db` (gorm) directly. Do not introduce repository
  interfaces (`AnalysisResultRepository`, `ProfileRepository`, etc.) —
  the existing ones are being removed.
- A repository abstraction over a single gorm-backed implementation adds
  a layer of mocks and parallel test paths without paying for itself.
  Integration tests against a real PostgreSQL test DB cover the same
  ground more honestly.
- When removing a repository, also remove the "broken repo returns 500"
  test cases — they exist only to exercise the abstraction.
