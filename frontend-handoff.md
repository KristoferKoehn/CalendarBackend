# Frontend Handoff

How to connect CalendarSite (the Next.js client) to this Go backend. The frontend codes against this API through an HTTP client; this backend is the real implementation.

The authoritative contract lives in CalendarSite: `backend-handoff.md`, `api-contract.md`, `project.md`, and `contract/out/openapi.json`. This file covers the practical connection steps.

## Run the backend

```powershell
go build -o CalendarBackend.exe ./cmd/server

$env:DATABASE_URL = 'postgres://calendar:PASSWORD@localhost:5432/calendarbackend?sslmode=disable'

./CalendarBackend.exe seed    # idempotent, creates seed data
./CalendarBackend.exe serve   # runs migrations at startup, listens on :4000
```

In production, the deploy pipeline builds, migrates, seeds, and restarts the Windows service automatically.

## Point the frontend at it

In CalendarSite `.env.local`:

```
NEXT_PUBLIC_API_URL=http://192.168.1.169:4000
```

Use `http://localhost:4000` when the frontend and backend run on the same machine.

## Ports and origins

- Backend REST and MCP: `:4000`
- Frontend dev: `http://localhost:3000` and LAN `http://192.168.1.169:3000`
- Frontend production: `http://couchcollective.software`

CORS is already configured to allow `http://localhost:3000`, `http://couchcollective.software`, and `http://192.168.1.169`.

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

| Method | Path | Access | Success |
|---|---|---|---|
| POST | `/auth/register` | public | 201 `{ "userId" }` |
| POST | `/auth/login` | public | 200 `{ "token", "expiresAt" }` |
| GET | `/auth/me` | user | 200 `User` |
| GET | `/resources` | user | 200 `{ "resources" }` |
| POST | `/resources` | admin | 201 `Resource` |
| PATCH | `/resources/{id}` | admin | 200 `Resource` |
| DELETE | `/resources/{id}` | admin | 200 `Resource` (active=false) |
| GET | `/users` | admin | 200 `{ "users" }` |
| PATCH | `/users/{id}` | admin | 200 `User` |
| POST | `/appointments` | user | 201 `Appointment` |
| GET | `/appointments` | user | 200 `{ "appointments" }` |
| POST | `/appointments/{id}/cancel` | owner or admin | 200 `Appointment` |
| POST | `/appointments/{id}/reschedule` | owner or admin | 200 `Appointment` |

`GET /appointments` filters: `resourceId`, `userId` (admin only), `from`, `to`, `status`. `from` and `to` are inclusive calendar dates in `America/Los_Angeles`.

## MCP

`POST /mcp` speaks JSON-RPC 2.0. Tools:

- `auth.register`, `auth.login` (public)
- `resources.list`, `appointments.create`, `appointments.cancel`, `appointments.reschedule`, `appointments.list` (bearer auth)

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

## Health check

`GET /healthz` returns `200 {"status":"ok"}`.

## Warning

The deploy pipeline runs integration tests against the live database. Those tests truncate `users`, `resources`, `appointments`, and `sessions`, then reseed the seed data. Any data created through the frontend is lost on every deploy. This is intentional for the MVP and must change before real users exist.
