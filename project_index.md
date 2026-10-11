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
| cmd/server/main.go | Entrypoint. Subcommands: migrate (works), serve (healthz only, blocks), seed (stub). | active |
| cmd/migrate/main.go | Standalone migrate CLI. Superseded by `CalendarBackend.exe migrate`; safe to delete. | legacy |
| internal/migrations/0001_init.sql | Schema: users, resources, appointments, sessions, btree_gist, exclusion constraint, indexes. | active |
| internal/migrations/migrations.go | Embedded migration runner; applies *.sql in order, records in schema_migrations. | active |
| internal/domain/domain.go | User, Resource, Appointment, Session types and role/status enums. | active |
| internal/apierror/apierror.go | Six error codes, HTTP status mapping, error envelope. | active |
| internal/config/config.go | Env config: DATABASE_URL, PORT, token TTL, CORS allowlist, seed flag. | active |
| internal/validation/validation.go | Input validation mirroring the Zod schemas. | active |
| internal/validation/validation_test.go | Table-driven validation tests. | active |
| internal/timeutil/timeutil.go | Resolve LA wall times to UTC, reject DST gap/overlap, format local ISO. | active |
| internal/timeutil/timeutil_test.go | Table-driven DST resolution tests. | active |
| internal/store/store.go | Repository interfaces: UserStore, ResourceStore, AppointmentStore, SessionStore. | active |
| execution-plan.md | Full build plan, phases 0-8. | active |
| project_index.md | This index. | active |

## Planned layout (fill in as files are created)

| Path | Purpose | Status |
|---|---|---|
| internal/store/postgres/ | pgx implementation. | planned |
| internal/auth/ | argon2id hashing, token issue/verify. | planned |
| internal/core/ | Use-case services. | planned |
| internal/httpapi/ | REST router, middleware, handlers. | planned |
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
