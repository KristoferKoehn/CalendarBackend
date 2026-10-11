# CalendarBackend Project Index

Single-page map of the Go backend for CalendarSite. Contract: CalendarSite
backend-handoff.md, api-contract.md, project.md, contract/schemas/*.ts.
Parity reference: CalendarSite/lib/api/mock.ts.

## Current files

| Path | Purpose | Status |
|---|---|---|
| go.mod | Module kristoferkoehn/CalendarBackend, go 1.27.2. Deps pgx/v5, google/uuid, x/crypto (indirect until go mod tidy). | active |
| go.sum | Dependency checksums. | active |
| start.bat | `go build` then run CalendarBackend.exe. Targets a root main package; update later to ./cmd/server. | active |
| .github/workflows/pull-calendar-backend.yml | Self-hosted Actions: pulls repo at C:\CalendarBackend, restarts CalendarBackend service via servy-cli. | active |
| internal/migrations/0001_init.sql | Schema: users, resources, appointments, sessions, btree_gist, exclusion constraint, indexes. | active |
| internal/migrations/migrations.go | Embedded migration runner; applies *.sql in order, records in schema_migrations. | active |
| cmd/migrate/main.go | CLI: reads DATABASE_URL, runs migrations. | active |
| execution-plan.md | Full build plan, phases 0-8. | active |
| project_index.md | This index. | active |

## Planned layout (fill in as files are created)

| Path | Purpose | Status |
|---|---|---|
| cmd/server/main.go | Wire config, DB, migrations, seed, REST + MCP. | planned |
| internal/domain/ | User, Resource, Appointment, Session types. | planned |
| internal/apierror/ | Error codes, status map, envelope. | planned |
| internal/config/ | Env config. | planned |
| internal/validation/ | Input validation. | planned |
| internal/timeutil/ | America/Los_Angeles wall-time resolution. | planned |
| internal/store/ | Repository interfaces. | planned |
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
