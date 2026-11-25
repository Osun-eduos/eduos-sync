# eduos-sync

**Sync service for the Osun EduOS Android and iOS apps.**

Part of [Osun EduOS](https://github.com/Osun-eduos/osun-eduos), the open-source education management and intelligence system for Osun State. Written in Go, as set out in the [languages and services decision](https://github.com/Osun-eduos/osun-eduos/blob/main/docs/decisions/0001-languages-and-services.md). It works alongside [eduos-api](https://github.com/Osun-eduos/eduos-api) and the same PostgreSQL database.

## What it does

Teachers use the mobile app where the internet is unreliable. The app keeps a copy of what the teacher needs, records every change as an event in an encrypted queue on the phone, and sends the queue when a connection returns. This service is the other end of that.

- **Snapshot.** `GET /v1/snapshot` returns the teacher's working set: the current session and term, the classes they are class teacher of (roll and the last 14 days of registers), and the subjects they teach (roll and this term's score sheet). It also returns the grading scales, the score split and the attendance rules, so the app can check entries offline.
- **Sync.** `POST /v1/sync` takes a batch of queued events and applies each one **exactly once**, even when a phone sends the same batch again after a dropped connection.

## How changes are applied

The school rules (who may change what, the 7-day register window, class rolls, score limits, approval and release) live in one place: **eduos-api**. This service does not write registers or scores itself. For each event it calls the matching eduos-api endpoint with the teacher's own access token, then records the outcome in the shared `SyncEvent` table.

| Event `type` | Payload | Applied through eduos-api |
| --- | --- | --- |
| `attendance.register` | `{ classId, date, marks: [{ studentId, status, note? }] }` | `PUT /attendance/register` |
| `scores.save` | `{ classSubjectId, scores: [{ studentId, marks: { CA1, CA2, EXAM } }] }` | `PUT /scoresheets` |
| `scores.submit` | `{ classSubjectId }` | `POST /scoresheets/:id/submit` |

Each event in the response gets one of three outcomes:

- **applied**: done. `duplicate: true` means it had already been applied earlier. The app removes it from its queue.
- **rejected**: eduos-api refused it, for example a register older than 7 days or scores for results already released. `message` explains why in plain words for the teacher. The app removes it from the queue and shows the message. Sending it again returns the same answer.
- **retry**: eduos-api could not be reached or was busy. That event and **every later event in the batch** are left for the next attempt, so changes are always applied in the order the teacher made them.

An expired access token stops the whole upload with `401`. The app refreshes its token (see *Sign-in* below) and tries again.

### Request format

```http
POST /v1/sync
Authorization: Bearer <access token from eduos-api>
X-Device-Id: <stable id for this installation, 8 to 64 characters>
Content-Type: application/json

{
  "events": [
    {
      "id": "6f1c2d0e-8a8b-4c39-9c1e-2b7a0f3d9e51",
      "type": "attendance.register",
      "occurredAt": "2026-11-25T08:05:00+01:00",
      "payload": { "classId": "…", "date": "2026-11-25", "marks": [{ "studentId": "…", "status": "PRESENT" }] }
    }
  ]
}
```

Event ids are UUIDs generated on the phone and never reused. At most 200 events go in one request.

## Sign-in

The app signs in with **eduos-api**, not with this service. It sends `X-Client: mobile` to `POST /auth/login`, and gets the refresh token in the response body instead of a cookie. It keeps that token in encrypted storage and calls `POST /auth/refresh` with `{ "refreshToken": "…" }` when the 15-minute access token expires. This service only checks access tokens, using the same secret as eduos-api. Only teachers can use it, and accounts still on a temporary password are refused until the password is changed.

## Run locally

You need Go 1.24 and a database migrated by eduos-api (`npx prisma migrate deploy` in eduos-api).

```sh
cp .env.example .env   # set the same JWT_ACCESS_SECRET as eduos-api
set -a; . ./.env; set +a
go run ./cmd/eduos-sync
curl localhost:4000/healthz
```

Or with Docker: `docker build -t eduos-sync .` and run it with the same variables.

## Tests

```sh
go test ./...
# Database tests (they erase the database's data):
SYNC_TEST_DATABASE_URL=postgresql://eduos:eduos@localhost:5432/eduos_test?schema=public go test ./internal/store/
```

The unit tests cover token checks, the exactly-once rule, rejections, ordered retries during outages, expired tokens and unknown event types. The database tests check the snapshot queries against the real schema. CI applies eduos-api's migrations first, so a schema change in eduos-api that breaks this service fails here.

## Layout

| Path | Holds |
| --- | --- |
| `cmd/eduos-sync` | Entry point |
| `internal/server` | HTTP handlers and the sync rules |
| `internal/store` | Snapshot queries and the `SyncEvent` table |
| `internal/apiclient` | Calls to eduos-api, and the cached subject list, grading scales and attendance rules |
| `internal/auth` | Access-token checks |

## Contributing

See the [contributing guide](https://github.com/Osun-eduos/.github/blob/main/CONTRIBUTING.md). Changes to the database schema are made in eduos-api, never here.

## Licence

[GNU Affero General Public License v3.0](LICENSE).
