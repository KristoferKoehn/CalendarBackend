# CalendarBackend Project Index

Single-page map of the Go backend for CalendarSite. Contract: CalendarSite
backend-handoff.md, api-contract.md, project.md, contract/schemas/*.ts.
Parity reference: CalendarSite/lib/api/mock.ts.

## Current files

| Path | Purpose | Status |
|---|---|---|
| go.mod | Module kristoferkoehn/CalendarBackend, go 1.27.2. Deps pgx/v5, google/uuid, x/crypto. | active |
| go.sum | Dependency checksums. | active |
| start.bat | Builds ./cmd/server to CalendarBackend.exe and runs it. Local dev helper. | active |
| .github/workflows/pull-calendar-backend.yml | Deploy on push: pull, set up Go, run tests, build, run migrations with DATABASE_URL secret, restart service via servy-cli. | active |
| cmd/server/main.go | Entrypoint. Subcommands: migrate, seed, serve (REST API + migrations). | active |
| cmd/migrate/main.go | Standalone migrate CLI. Superseded by `CalendarBackend.exe migrate`; safe to delete. | legacy |
| internal/migrations/0001_init.sql | Schema: users, resources, appointments, sessions, btree_gist, exclusion constraint, indexes. | active |
| internal/migrations/migrations.go | Embedded migration runner; applies *.sql in order, records in schema_migrations. | active |
| internal/domain/domain.go | User, Resource, Appointment, Session types and role/status enums. | active |
| internal/apierror/apierror.go | Six error codes, HTTP status mapping, error envelope, IsCode helper. | active |
| internal/apierror/apierror_test.go | Error code and envelope tests. | active |
| internal/config/config.go | Env config: DATABASE_URL, PORT, token TTL, CORS allowlist, seed flag. | active |
| internal/validation/validation.go | Input validation mirroring the Zod schemas. | active |
| internal/validation/validation_test.go | Table-driven validation tests. | active |
| internal/timeutil/timeutil.go | Resolve LA wall times to UTC, reject DST gap/overlap, format local ISO, day boundaries. | active |
| internal/timeutil/timeutil_test.go | Table-driven DST resolution tests. | active |
| internal/store/store.go | Repository interfaces: UserStore, ResourceStore, AppointmentStore, SessionStore. | active |
| internal/store/postgres/ | pgx implementation: Store, scan helpers, error mapping, users, resources, appointments, sessions. | active |
| internal/seed/seed.go | Idempotent mock-parity seed: admin/user users, Room A, Room B. | active |
| internal/core/core.go | Shared input types, LoginResult, authorization helpers, now default. | active |
| internal/core/auth.go | AuthService: register, login. | active |
| internal/core/resources.go | ResourceService: list, create, update, deactivate. | active |
| internal/core/users.go | UserService: list, update. | active |
| internal/core/appointments.go | AppointmentService: create, list, cancel, reschedule. | active |
| internal/core/fakes_test.go | In-memory store fakes for tests. | active |
| internal/core/auth_test.go | AuthService tests. | active |
| internal/core/resources_test.go | ResourceService tests. | active |
| internal/core/users_test.go | UserService tests. | active |
| internal/core/appointments_test.go | AppointmentService tests. | active |
| internal/store/memory/memory.go | In-memory store implementations for tests. | active |
| internal/httpapi/handler.go | Deps, NewHandler, route table, JSON and error writers. | active |
| internal/httpapi/middleware.go | CORS, request logging, panic recovery. | active |
| internal/httpapi/auth.go | Bearer auth middleware. | active |
| internal/httpapi/context.go | Authenticated user context helpers. | active |
| internal/httpapi/dto.go | Request/response DTOs and validation helpers. | active |
| internal/httpapi/handlers_auth.go | register, login, me handlers. | active |
| internal/httpapi/handlers_resources.go | Resource handlers. | active |
| internal/httpapi/handlers_users.go | User handlers. | active |
| internal/httpapi/handlers_appointments.go | Appointment handlers. | active |
| internal/httpapi/handlers_test.go | Full-stack handler tests. | active |
| internal/auth/password.go | argon2id password hash and verify (PHC encoded). | active |
| internal/auth/token.go | Opaque token generation and SHA-256 hashing. | active |
| internal/auth/password_test.go | Password hashing tests. | active |
| internal/auth/token_test.go | Token hashing tests. | active |
| execution-plan.md | Full build plan, phases 0-8. | active |
| project_index.md | This index. | active |

## Planned layout (fill in as files are created)

| Path | Purpose | Status |
|---|---|---|
| internal/mcp/ | MCP over HTTP, JSON-RPC, tools. | planned |

## Key invariants

- Port 4000, REST + MCP.
- PostgreSQL; overlap via exclusion constraint (needs btree_gist).
- Bearer token 24h, stored hashed in sessions.
- argon2id; email trim + lowercase; password min 8.
- Input date + HH:mm in America/Los_Angeles; output UTC Z + Seattle offset ISO.
- DST gap/overlap -> INVALID_INPUT.
- Error envelope { "error": { "code", "message", "details"? } }.
- Seed parity: admin@example.com, user@example.com (password123), Room A, Room B.
