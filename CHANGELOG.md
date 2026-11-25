# Changelog

## [0.2.0] - 2026-11-25

### Added
- The snapshot includes the current session's holidays, so the app does not offer registers on those days.

## [0.1.1] - 2026-11-25

### Changed
- `API_URL` may be a private-network `host:port`, as Render provides; `http://` is added.

## [0.1.0] - 2026-11-25

### Added
- Sync service for the mobile apps: `GET /v1/snapshot` (a teacher's offline working set) and `POST /v1/sync` (queued changes from a phone, applied once each through eduos-api).
- Access-token checks matching eduos-api, idempotency records in the shared `SyncEvent` table, ordered retries during outages.
- Unit tests, database tests against the eduos-api schema, Dockerfile and CI.
