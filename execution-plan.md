# CalendarBackend Execution Plan

Backend for CalendarSite. Written in Go.

> **Status: historical build plan.** All phases are implemented. This file is kept
> for phase history and rationale; it is not a current task list. Corrections made
> during the documentation audit are marked **Correction** inline.

## Contract sources

- `CalendarSite/backend-handoff.md` (authoritative summary)
- `CalendarSite/api-contract.md`
- `CalendarSite/project.md` (historical MVP spec)
- `CalendarSite/contract/schemas/*.ts` (exact shapes)
- `CalendarSite/contract/out/openapi.json` (machine-readable contract — **generated
  and git-ignored; run `npm run contract:generate` in CalendarSite first**)

## Locked constraints

- Go 1.27.2 (do not lower).
- PostgreSQL.
- Port `127.0.0.1:4001` for REST and MCP.
- Bearer tokens, 24 hour lifetime, stored hashed in sessions.
- argon2id password hashing. Email trimmed and lowercased. Password min 8.
- Input times are date + HH:mm in America/Los_Angeles.
- Output has UTC fields (Z suffix) and local fields with Seattle offset.
- Spring-forward nonexistent and fall-back ambiguous times return INVALID_INPUT.
- Overlap is half-open [startAt, endAt), per resource, booked only, enforced by DB constraint.

## Progress

- Phase 0 complete: module renamed to kristoferkoehn/CalendarBackend, deps added,
  hello.go removed, hand-rolled migration runner chosen.
- Phase 3 complete: migrations, runner, store interfaces, pgx adapter, and seed.
- Phase 1 complete: domain types, error model, config, and validation are in place.
- Phase 2 complete: America/Los_Angeles wall-time resolution with DST gap/overlap
  detection and local ISO formatting, plus table-driven tests.
- Store interfaces defined in internal/store.
- Auth complete: argon2id password hashing and opaque token hashing, with tests.
- pgx store implementation complete: all four stores with SQLSTATE error mapping.
- Phase 5 complete: all core services (auth, resources, users, appointments)
  with in-memory fakes and tests.
- Phase 6 complete: REST transport with middleware, all 15 REST endpoints, and
  full-stack handler tests; serve runs migrations and serves the API.
  **Correction:** the original text said 13; the two later additions were
  `GET /healthz` and `DELETE /users/{id}`.
- Phase 7 complete: MCP over HTTP with seven tools and JSON-RPC error mapping.
- Phase 8 complete: PostgreSQL integration tests are written and wired into the
  workflow. **Correction:** they run against a dedicated database named with a
  `_test` suffix via `TEST_DATABASE_URL`, not against production. The suite refuses
  to run otherwise, and skips (exit 0) when the variable is unset.
- Remaining: OpenAPI spot-check and frontend E2E. Neither blocks the MVP.

## Phase 0: Repo scaffold

1. Rewrite go.mod: change module path from example/hello to a real path.
   Keep go 1.27.2. Done.
2. Remove hello.go. Create cmd/server/main.go when wiring starts. Done (removal).
3. Dependencies: pgx/v5, google/uuid, x/crypto. Hand-rolled embedded migration
   runner. net/http with Go 1.22+ ServeMux for routes. Done.

## Phase 1: Foundations

4. Domain types in internal/domain: User, Resource, Appointment, Session,
   Role, AppointmentStatus.
5. Error model in internal/apierror: six codes, HTTP status mapping, and the
   envelope { "error": { "code", "message", "details"? } } with details
   omitted when absent.
6. Config in internal/config: DATABASE_URL, PORT (default 4001, bound to 127.0.0.1), token TTL (24h),
   CORS allowlist, seed flag.
7. Validation in internal/validation mirroring the Zod schemas:
   - email trim + lowercase
   - UUID format
   - date YYYY-MM-DD, time HH:mm in 24-hour format
   - password min 8
   - role user|admin, status booked|cancelled
   - endTime strictly after startTime
   - PATCH /users/{id} requires at least one of role or isAgent

## Phase 2: Timezone core

8. internal/timeutil loads America/Los_Angeles once. Embed tzdata with
   import _ "time/tzdata".
9. Wall-clock resolver returns one UTC instant and detects:
   - nonexistent times (spring forward) -> INVALID_INPUT
   - ambiguous times (fall back) -> INVALID_INPUT
   Technique: build time.Date(y,m,d,hh,mm,0,0,loc), verify the formatted
   wall clock matches input, check the offset one hour on either side.
10. Local output: startAtLocal/endAtLocal as ISO with Seattle offset,
    for example 2026-10-10T14:00:00-07:00. startAt/endAt as UTC with Z.
11. Table-driven tests for 2026-03-08, 2026-11-01, and normal dates.

## Phase 3: Persistence

12. Embedded migrations in internal/migrations:
    - users, resources, appointments, sessions per project.md section 4
    - CREATE EXTENSION IF NOT EXISTS btree_gist (required for uuid WITH =)
    - exclusion constraint (see 0001_init.sql)
13. Repository interfaces in internal/store: UserStore, ResourceStore,
    AppointmentStore, SessionStore.
14. PostgreSQL adapter in internal/store/postgres using pgxpool. DB enforces
    overlap; app pre-checks to return a clean CONFLICT.
15. Idempotent seed, matching the contract:
    - admin@example.com / password123, admin
    - user@example.com / password123, user
    - Room A (Main conference room), Room B (Small meeting room), both active,
      America/Los_Angeles

## Phase 4: Auth core

16. argon2id with random per-user salt, constant-time compare. Never log
    plaintext or hashes.
17. Opaque token: crypto-random, store SHA-256 hash in sessions with
    expires_at, verify by hashing the presented token.
18. Email normalized trim + lowercase before storage and comparison.
19. 24h expiry checked on every authenticated request.

## Phase 5: Core services

20. Use cases in internal/core, one responsibility each, depending only on
    repository interfaces:
    - Register (duplicate email -> CONFLICT, returns userId)
    - Login (wrong credentials -> UNAUTHENTICATED, returns token + expiresAt)
    - Me
    - ListResources (active only)
    - CreateResource, UpdateResource (partial), DeactivateResource (admin)
    - ListUsers, UpdateUser (admin)
    - CreateAppointment (resource exists+active, resolve times, CONFLICT overlap)
    - ListAppointments (every authenticated user sees all appointments, with an
      optional userId filter; filters resourceId, title, status, from, to)
      **Correction:** originally non-admin self-scoped. That restriction was
      removed; write access remains owner-or-admin only.
    - CancelAppointment (owner/admin, CONFLICT if already cancelled)
    - RescheduleAppointment (owner/admin, create validation, exclude self)
21. Authorization inside services so REST and MCP share it. Non-admin on
    admin operations -> FORBIDDEN.
22. Unit test each service against in-memory fakes.

## Phase 6: REST transport

23. internal/httpapi: ServeMux, CORS allowlist, bearer auth middleware,
    log/slog logging, panic recovery.
    **Correction:** the default allowlist is now `https://couchcollective.software`
    only. The previously listed local origins (`http://localhost:3000`,
    `http://127.0.0.1:3000`, `http://192.168.1.169`) were removed; add them back via
    the `CORS_ORIGINS` environment variable when developing locally.
24. Handlers in internal/httpapi/handlers, thin decode -> service -> encode,
    map errors to the envelope.

**Correction:** the implemented route table has **16 routes**, not 13 — the
original list below omitted `GET /healthz` and `DELETE /users/{id}`.

| Method | Path | Success |
|---|---|---|
| GET | /healthz | 200 { "status": "ok" } |
| POST | /auth/register | 201 { userId } |
| POST | /auth/login | 200 { token, expiresAt } |
| GET | /auth/me | 200 User |
| GET | /resources | 200 { resources } |
| POST | /resources | 201 Resource |
| PATCH | /resources/{id} | 200 Resource |
| DELETE | /resources/{id} | 200 Resource (active=false) |
| GET | /users | 200 { users } |
| PATCH | /users/{id} | 200 User |
| DELETE | /users/{id} | 200 User |
| POST | /appointments | 201 Appointment |
| GET | /appointments | 200 { appointments } |
| POST | /appointments/{id}/cancel | 200 Appointment |
| POST | /appointments/{id}/reschedule | 200 Appointment |
| POST | /mcp | JSON-RPC 2.0 |

25. GET /appointments from/to are inclusive calendar dates in
    America/Los_Angeles; convert to a UTC range over local day boundaries
    and filter startAt.
26. Handler tests with httptest and fake services.

## Phase 7: MCP over HTTP

27. JSON-RPC 2.0 dispatcher at POST /mcp: initialize, tools/list, tools/call.
    **Correction:** `ping` is also implemented, and there is **no session-id
    header**. The endpoint is stateless, accepts only POST, and advertises protocol
    version `2024-11-05`.
28. Tools in internal/mcp, each mapped to the same core services:
    - auth.register, auth.login (public)
    - resources.list, appointments.create, appointments.cancel,
      appointments.reschedule, appointments.list (bearer)
    appointments.create returns appointmentId, status, startAt, endAt.
    **Correction:** `appointments.list` accepts `resourceId`, `userId`, `title`,
    `status`, `from`, and `to` — the same filters as `GET /appointments` (it gained
    `userId` and `status` alongside the shared-visibility change). Appointments are
    visible to every authenticated user, not only their owner.
29. Core errors become JSON-RPC errors carrying the contract code in data.
30. MCP tests against fakes.

## Phase 8: Integration and conformance

31. Wire cmd/server/main.go: config, pool, migrations, optional seed, REST + MCP.
32. Integration tests against PostgreSQL: overlap constraint, DST, auth, roles.
33. Generate CalendarSite contract/out/openapi.json and spot-check paths,
    status codes, and shapes.
34. Frontend E2E: NEXT_PUBLIC_API_URL=http://127.0.0.1:4001,
    exercise register, login, resources, appointments, admin pages.

## SOLID mapping

- SRP: handlers transport only; validation, timezone, hashing, persistence
  each live in one package; services hold use-case logic.
- OCP: new MCP tools or REST endpoints extend transport without touching core.
- LSP: repository fakes implement the same interfaces as the pg adapter.
- ISP: small per-entity store interfaces.
- DIP: core depends on interfaces; main.go injects concrete implementations.

## Gotchas

- GET /resources returns active resources only.
- Booking against an unknown or inactive resource returns NOT_FOUND, not FORBIDDEN.
- GET /appointments is readable by every authenticated user; `userId` is an
  optional filter available to anyone.
- Write access is owner-or-admin only: a non-admin cannot cancel or reschedule
  another user's appointment (403).
- Resource and user administration remains admin-only.
- `title` is required and trimmed, 1-200 characters. Appointment `description` is
  trimmed, max 2000, and is NOT NULL defaulting to '' — unlike resource
  `description`, which is nullable. Omit it on create to store the empty string.
- DELETE /users/{id} is a hard delete and returns CONFLICT for self, the last
  admin, or a user with appointments.
- Cancel/reschedule of an already-cancelled appointment -> CONFLICT
  "Already cancelled".
- Overlap interval is [startAt, endAt), booked only, DB-enforced.
- Only POST /mcp is accepted; there is no session-id header.
- GET /appointments `from`/`to` are inclusive local calendar dates; internally
  `to` becomes local midnight of the *next* day (exclusive upper bound).
