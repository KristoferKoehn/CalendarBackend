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

The complete route table is 20 routes: 19 REST plus `POST /mcp`.

| Method | Path | Access | Success |
|---|---|---|---|
| GET | `/healthz` | public | 200 `{ "status": "ok" }` |
| POST | `/auth/register` | public | 201 `{ "userId" }` |
| POST | `/auth/login` | public | 200 `{ "token", "expiresAt" }` |
| POST | `/auth/forgot-password` | public | 204, no body. Rate limited. **Always** the same answer, whether or not the address has an account |
| POST | `/auth/reset-password` | public | 204, no body. Body `{ "token", "newPassword" }`. Ends every existing session for the account |
| POST | `/auth/verify-email` | public | 200 `User`. Body `{ "token" }`. No session needed: the link opens from a mail client |
| POST | `/auth/resend-verification` | public | 204, no body. Body `{ "email" }`. Same answer for an unknown address; `409` when already confirmed |
| GET | `/auth/me` | user | 200 `User` |
| PATCH | `/auth/me` | user | 200 `User` (own profile) |
| DELETE | `/auth/me` | user | 200 `User` (the deleted user) |
| GET | `/resources` | user | 200 `{ "resources" }` |
| POST | `/resources` | admin | 201 `Resource` |
| PATCH | `/resources/{id}` | admin | 200 `Resource` |
| DELETE | `/resources/{id}` | admin | 200 `Resource` (active=false) |
| GET | `/users` | user | 200 `{ "users" }` |
| GET | `/users/{id}` | user | 200 `User` |
| PATCH | `/users/{id}` | admin | 200 `User` |
| DELETE | `/users/{id}` | admin | 200 `User` (the removed user) |
| POST | `/appointments` | user | 201 `Appointment` |
| GET | `/appointments` | user | 200 `{ "appointments" }` |
| DELETE | `/appointments/{id}` | admin | 200 `{ "status": "deleted", "id" }` |
| POST | `/appointments/{id}/cancel` | owner or admin | 200 `Appointment` |
| POST | `/appointments/{id}/reschedule` | owner or admin | 200 `Appointment` |

`GET /appointments` returns **all appointments to every authenticated user**. Filters: `resourceId`, `userId`, `title`, `from`, `to`, `status`. `from` and `to` are inclusive calendar dates in `America/Los_Angeles`; `title` is a case-insensitive substring match. `userId` narrows to one owner and may be used by any caller.

Read access is shared; write access is not — only the owner or an admin may cancel or
reschedule an appointment, and only an admin may delete one outright. Owners cancel
rather than erase, which is why an owner cannot `DELETE` their own appointment.

Appointments carry `ownerEmail` (the owner's email captured at creation) and
`ownerName` (their display name at that moment, or null). Both are always present,
and they survive the owner's account being deleted — render `ownerName` and fall
back to `ownerEmail`. Do not resolve the owner via `userId`, which is a historical
reference that may match no user. `ownerName` survives a reschedule.

User profiles are a shared directory: any authenticated user may list every user and
read any single profile. A user edits their own details with `PATCH /auth/me`, which
takes no id and cannot change `email`, `role`, or `isAgent`. Only role and agent
changes remain admin-only.

`DELETE /users/{id}` is a hard delete and **leaves the user's appointments in
place**. It returns `409 CONFLICT` when the target is the caller or the last
remaining admin — no longer for "a user with appointments".

`DELETE /auth/me` lets any authenticated user delete their own account; the last
admin cannot. `DELETE /appointments/{id}` (admin only) is how appointments left
behind by a deleted account are cleared.

There is no `GET /appointments/{id}`; the frontend locates the target client-side.

## MCP

`POST /mcp` speaks JSON-RPC 2.0 over `POST` only. There is no session-id header —
the stateless request/response form is what was implemented, despite the "Streamable
HTTP" framing in the build plan.

Methods: `initialize` (protocol version `2024-11-05`), `tools/list`, `tools/call`,
and `ping`. Tools, exactly eight:

- `auth.register`, `auth.login` (public)
- `resources.list`, `appointments.create`, `appointments.cancel`, `appointments.reschedule`, `appointments.list` (bearer auth)
- `appointments.delete` (bearer auth, **admin accounts only**)

`appointments.list` accepts `resourceId`, `userId`, `title`, `status`, `from`, and
`to` — the same filters as `GET /appointments`. Appointments are visible to every
authenticated user, so an agent can read all bookings.

`appointments.delete` hard-deletes an appointment. It is how appointments left
behind by a deleted user are cleared; a non-admin token gets `FORBIDDEN`.

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

Codes: `INVALID_INPUT` (400), `UNAUTHENTICATED` (401), `FORBIDDEN` (403), `NOT_FOUND` (404), `CONFLICT` (409), `RATE_LIMITED` (429), `INTERNAL` (500).

`RATE_LIMITED` is returned by `/auth/login` and `/auth/register` after too many
failed attempts, and carries a `Retry-After` header in seconds. The account limit is
checked before the password is compared; the caller limit only after a failure, so a
correct password always succeeds even from an address with accrued failures.

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
| `RATE_LIMIT_LOGIN_FAILURES` | `5` | Failed sign-ins tolerated per account or address per window. An **attempt** limit: tight on purpose |
| `RATE_LIMIT_REGISTER_FAILURES` | `10` | Sign-ups tolerated per address per window. An **action** limit: generous, because a legitimate sign-up is not a failure |
| `RATE_LIMIT_WINDOW_SECONDS` | `900` | Sliding window for every limit |
| `PASSWORD_RESET_TTL_MINUTES` | `30` | How long a reset link stays valid |
| `RATE_LIMIT_FORGOT_PASSWORD_FAILURES` | `10` | Reset requests tolerated per address or account per window |
| `RATE_LIMIT_RESET_PASSWORD_FAILURES` | `10` | Reset redemptions tolerated per address per window |
| `RATE_LIMIT_RESEND_VERIFICATION_FAILURES` | `20` | Confirmation emails tolerated per address or account per window |
| `EMAIL_VERIFICATION_TTL_HOURS` | `24` | How long a confirmation link stays valid |
| `TRUST_PROXY_HEADERS` | *(unset)* | `true` identifies callers by `X-Forwarded-For` / `X-Real-IP`. Off by default; the header is client-supplied |
| `MAIL_PROVIDER` | `stub` | `stub` (records, sends nothing) or `resend` |
| `RESEND_API_KEY` | *(unset)* | Resend secret. Required when `MAIL_PROVIDER=resend`. Never logged |
| `MAIL_FROM` | *(none — required by `resend`)* | Envelope sender. **No default on purpose**: the address must sit on a domain verified with the provider, and a plausible default on an unverified domain would be accepted locally and rejected on the first send |
| `MAIL_REPLY_TO` | *(unset)* | Optional reply address. Without it, replies go to `MAIL_FROM` |
| `PUBLIC_SITE_URL` | `https://couchcollective.software` | Origin used to build links inside emails. **No port**: these links are opened from a mail client, not from the browser talking to the API |

**These must be set on the running service, not in the workflow.** The deploy workflow's
`env` block applies only to the step it is attached to; a Windows service does not
inherit it. Servy keeps a service's environment variables in its own configuration
(`servy-cli show --name='CalendarBackend' --decrypt` to inspect, `--envVars` at install
time to set). Verify with `CalendarBackend.exe mailtest you@example.com`.
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
