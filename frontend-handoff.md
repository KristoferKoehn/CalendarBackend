# Frontend Handoff

How to connect CalendarSite (the Next.js client) to this Go backend. The frontend codes against this API through an HTTP client; this backend is the real implementation.

The authoritative contract lives in CalendarSite: `backend-handoff.md`, `api-contract.md`, `project.md`, and `contract/out/openapi.json`. This file covers the practical connection steps.

## Run the backend

```powershell
go build -o CalendarBackend.exe ./cmd/server

$env:DATABASE_URL = 'postgres://calendar:PASSWORD@localhost:5432/calendarbackend?sslmode=disable'

./CalendarBackend.exe seed    # idempotent, creates seed data
./CalendarBackend.exe serve   # runs migrations at startup, listens on 127.0.0.1:4001
```

In production, the deploy pipeline builds, migrates, seeds, and restarts the Windows service automatically.

## Point the frontend at it

In CalendarSite `.env.local`:

```
NEXT_PUBLIC_API_URL=http://127.0.0.1:4001
```

Use `http://127.0.0.1:4001` when the frontend and backend run on the same machine.

## Ports and origins

| Environment | Backend REST + MCP | Frontend |
|---|---|---|
| Local dev | `127.0.0.1:4001` (config default, loopback) | `http://127.0.0.1:3000` |
| Deployed | `https://couchcollective.software:4000` | `https://couchcollective.software` |

REST and MCP share one listener, and `GET /healthz` is an unauthenticated liveness
probe on it.

The two backend ports are different environments, not a conflict. `4001` comes from
the `PORT` default in `internal/config/config.go` and is bound to loopback only —
it is not reachable from outside. Deployments set `PORT=4000` (or proxy 4000 to the
local listener); `https://couchcollective.software:4000/healthz` returns
`200 {"status":"ok"}`.

Because `NEXT_PUBLIC_API_URL` is inlined at build time, the hosted frontend is
pinned to whatever value was set when it was built, and changing it requires a
rebuild.

CORS default allowlist is **`https://couchcollective.software` only**
(`internal/config/config.go`), extendable via the comma-separated `CORS_ORIGINS`
environment variable.

> An earlier revision of this file listed four origins — `http://localhost:3000`,
> `http://127.0.0.1:3000`, `http://couchcollective.software`, and
> `http://192.168.1.169`. That is no longer the default; the local origins were
> removed. Set `CORS_ORIGINS` to call the API from a local dev server.

The CORS layer only sets headers for allowed origins and short-circuits `OPTIONS`
with `204`; it does not reject other origins, and non-browser clients ignore CORS.

## Seed data

| Account | Password | Role |
|---|---|---|
| admin@example.com | password123 | admin |
| user@example.com | password123 | user |

Resources: `Room A` (Main conference room), `Room B` (Small meeting room), both active, `America/Los_Angeles`.

## Authentication

1. `POST /auth/register` with `{ "email", "password" }` returns `201 { "userId" }`.
2. `POST /auth/login` returns `200 { "token", "expiresAt" }`. Tokens last 24 hours.
3. Store the token in `localStorage`.
4. Send `Authorization: Bearer <token>` on every authenticated request.

## REST endpoints

The complete route table is 16 routes: 15 REST plus `POST /mcp`.

| Method | Path | Access | Success |
|---|---|---|---|
| GET | `/healthz` | public | 200 `{ "status": "ok" }` |
| POST | `/auth/register` | public | 201 `{ "userId" }` |
| POST | `/auth/login` | public | 200 `{ "token", "expiresAt" }` |
| GET | `/auth/me` | user | 200 `User` |
| GET | `/resources` | user | 200 `{ "resources" }` |
| POST | `/resources` | admin | 201 `Resource` |
| PATCH | `/resources/{id}` | admin | 200 `Resource` |
| DELETE | `/resources/{id}` | admin | 200 `Resource` (active=false) |
| GET | `/users` | admin | 200 `{ "users" }` |
| PATCH | `/users/{id}` | admin | 200 `User` |
| DELETE | `/users/{id}` | admin | 200 `User` (the removed user) |
| POST | `/appointments` | user | 201 `Appointment` |
| GET | `/appointments` | user | 200 `{ "appointments" }` |
| POST | `/appointments/{id}/cancel` | owner or admin | 200 `Appointment` |
| POST | `/appointments/{id}/reschedule` | owner or admin | 200 `Appointment` |

`GET /appointments` filters: `resourceId`, `userId` (admin only), `title`, `from`, `to`, `status`. `from` and `to` are inclusive calendar dates in `America/Los_Angeles`; `title` is a case-insensitive substring match.

`DELETE /users/{id}` is a hard delete. It returns `409 CONFLICT` when the target is
the caller, the last remaining admin, or a user with any appointments.

There is no `GET /appointments/{id}`; the frontend locates the target client-side.

## MCP

`POST /mcp` speaks JSON-RPC 2.0 over `POST` only. There is no session-id header —
the stateless request/response form is what was implemented, despite the "Streamable
HTTP" framing in the build plan.

Methods: `initialize` (protocol version `2024-11-05`), `tools/list`, `tools/call`,
and `ping`. Tools, exactly seven:

- `auth.register`, `auth.login` (public)
- `resources.list`, `appointments.create`, `appointments.cancel`, `appointments.reschedule`, `appointments.list` (bearer auth)

Contract errors become JSON-RPC errors with code `-32000` and the contract code in
`error.data.code`.

## Error envelope

```json
{
  "error": {
    "code": "CONFLICT",
    "message": "Overlapping booking"
  }
}
```

Codes: `INVALID_INPUT` (400), `UNAUTHENTICATED` (401), `FORBIDDEN` (403), `NOT_FOUND` (404), `CONFLICT` (409), `INTERNAL` (500).

`NOT_FOUND` is also what a deactivated or unknown resource returns on booking —
not `FORBIDDEN` or `CONFLICT`.

## Health check

`GET /healthz` returns `200 {"status":"ok"}`. It is unauthenticated and is not
registered in the frontend's generated OpenAPI document.

## Environment variables

| Variable | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | *(required)* | Server exits if unset |
| `PORT` | `4001` | Bound as `127.0.0.1:<PORT>` |
| `TOKEN_TTL_HOURS` | `24` | Bearer token lifetime |
| `CORS_ORIGINS` | `https://couchcollective.software` | Comma-separated allowlist |
| `SEED` | *(unset)* | `SEED=true` seeds at serve time |

## About `start.bat`

`start.bat` only *runs* an already-built `CalendarBackend.exe`, redirecting output
to `service.log`. It does not build. Build first with
`go build -o CalendarBackend.exe ./cmd/server`.

## Warning

The deploy pipeline runs integration tests, but **against a dedicated test
database, not production**. The integration suite requires `TEST_DATABASE_URL`
and refuses to run unless the target database name ends in `_test`.
