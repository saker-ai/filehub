# FileHub

FileHub is a local asset service with a Go API, embedded web UI, metadata search, signed download URLs, and local/S3/OSS storage backends.

## Run

Create `.env` when local overrides are needed, then run:

```bash
make run
```

`make run` automatically includes `.env`, builds the frontend and backend, creates runtime directories, and starts `./filehub`. It writes the running PID to `.synapse/stack/filehub.pid`.

Before starting, `make run` checks the configured listen port from `FILEHUB_ADDR`. It stops a previous FileHub process recorded in the PID file or already listening on the port. If the port is owned by another process, it fails instead of killing an unrelated service.

## Configuration

Common environment variables:

```bash
FILEHUB_ADDR=:17040
FILEHUB_DSN=sqlite://.synapse/stack/filehub.db
FILEHUB_STORAGE_BACKEND=osfs
FILEHUB_STORAGE_DIR=.synapse/stack/filehub-data
FILEHUB_API_KEY_AUTH_ENABLED=false
FILEHUB_API_KEYS=
FILEHUB_PRESIGN_SECRET=change-me
FILEHUB_PID_FILE=.synapse/stack/filehub.pid
```

Workspace Sync is opt-in. When enabled, FileHub serves the `/v1/workspaces`
API described below:

```bash
FILEHUB_WORKSPACES_ENABLED=true
# Optional: lower (never raise) the documented hard limits
FILEHUB_WORKSPACES_MAX_COMMIT_BODY_BYTES=2097152
FILEHUB_WORKSPACES_MAX_COMMIT_OPERATIONS=1000
FILEHUB_WORKSPACES_MAX_PATH_BYTES=4096
FILEHUB_WORKSPACES_MAX_PATH_SEGMENT_BYTES=255
FILEHUB_WORKSPACES_MAX_NOTE_BYTES=1024
FILEHUB_WORKSPACES_MAX_READ_EVENT_BATCH=1000
```

OSS uses the `FILEHUB_OSS_*` names:

```bash
FILEHUB_STORAGE_BACKEND=oss
FILEHUB_OSS_ENDPOINT=https://oss-cn-shanghai-internal.aliyuncs.com
FILEHUB_OSS_PUBLIC_ENDPOINT=https://oss-cn-shanghai.aliyuncs.com
FILEHUB_OSS_REGION=cn-shanghai
FILEHUB_OSS_BUCKET=your-bucket
FILEHUB_OSS_ACCESS_KEY=your-access-key
FILEHUB_OSS_SECRET_KEY=your-secret-key
```

S3 uses `FILEHUB_S3_*` names with the same shape:

```bash
FILEHUB_STORAGE_BACKEND=s3
FILEHUB_S3_ENDPOINT=http://127.0.0.1:9000
FILEHUB_S3_PUBLIC_ENDPOINT=https://assets.example.com
FILEHUB_S3_REGION=us-east-1
FILEHUB_S3_BUCKET=filehub
FILEHUB_S3_ACCESS_KEY=your-access-key
FILEHUB_S3_SECRET_KEY=your-secret-key
```

## Search

The asset list API supports regular filters and metadata filters:

```bash
curl 'http://127.0.0.1:17040/v1/assets?filename=demo&tags=cover&meta_key=model&meta_value=gpt-image-1'
```

Metadata lookup is backed by an indexed `asset_metadata` table, so exact metadata key/value filters no longer scan the JSON text column. Existing assets are backfilled into the index on startup when needed.

List responses include cursor pagination:

```json
{
  "object": "list",
  "data": [],
  "has_more": false,
  "next_cursor": ""
}
```

Pass `cursor=<next_cursor>` to fetch the next page. The web asset page uses these metadata filters and cursor pagination, keeps filters in the URL, and debounces search input.

## Signed URLs

The default signed URL TTL is `7d` (`168h`). For S3 and OSS, FileHub returns
provider-native signed URLs. When the service writes through an internal endpoint,
set `FILEHUB_S3_PUBLIC_ENDPOINT` or `FILEHUB_OSS_PUBLIC_ENDPOINT` so returned URLs
use the public endpoint while server-side storage traffic keeps using the internal one.
Request a shorter TTL with:

```bash
curl -X POST 'http://127.0.0.1:17040/v1/assets/{id}/presign' \
  -H 'Content-Type: application/json' \
  -d '{"expires_in":"5m"}'
```

Both `POST /v1/assets/{id}/presign` and
`POST /v1/external/assets/{id}/presign` use the same provider-native public
endpoint and 7-day default.

Set a non-default `FILEHUB_PRESIGN_SECRET` before exposing fallback signed URLs outside a trusted local environment.

## External asset API compatibility

FileHub exposes the generic external asset contract at `/v1/external`. Point clients such as Saker at this base URL:

```text
X-Saker-Storage-Uri: https://filehub.example.com/v1/external
X-Saker-Storage-Uri: https://filehub.example.com/v1/external?upload_mode=direct&json_naming=snake_case
X-Saker-Storage-Uri: https://filehub.example.com/v1/external?upload_mode=direct_multipart&json_naming=snake_case
```

The first form keeps proxy upload as the compatible default. Use the second
form only with an S3 or OSS backend when Saker should upload directly to the
provider's public endpoint.

The compatibility endpoints are `POST|PUT /assets`, `GET|HEAD /assets/{id}`, and
`POST /assets/{id}/presign`. Upload responses use the generic camel-case fields
`id`, `url`, `contentType`, `size`, and `expiresAt`; `url` is a provider-native
signed URL for S3/OSS and a FileHub signed URL for local storage. Its default
validity is 7 days. FileHub's richer metadata API remains under `/v1/assets`.

When the backend is S3 or OSS, clients can bypass FileHub's data plane with the
optional direct-upload flow:

`GET /capabilities` advertises single/multipart support, the upload limit,
part-size bounds, and supported checksum algorithms. Clients must select
`direct` or `direct_multipart` explicitly; FileHub does not silently fall back.

1. `POST /uploads` with `mode=direct`, `filename`, `purpose`, `contentType`, and
   `totalBytes` creates a session and returns `uploadId`, `assetId`, `method`,
   `url`, and provider-required `headers`.
2. Send the bytes once to the returned provider URL using the returned method
   and headers. Do not forward FileHub authentication to that URL.
3. `POST /uploads/{uploadId}/complete` with `checksum=sha256:<hex>` verifies and registers the
   provider object, returning the normal generic asset result.

Provider upload URLs always target a session-owned staging key. Completion
verifies that object, promotes it to the stable asset key, and removes the
staging object. Reusing an unexpired upload URL therefore cannot overwrite the
registered asset. Promotion and completion are retry-safe after process crashes.

For `mode=direct_multipart`, use
`POST /uploads/{uploadId}/parts/{part}/presign` for every provider part and pass
the returned ETags in the completion request. Completion is idempotent: retries
return the previously registered asset. Expired unfinished sessions abort native
multipart uploads or delete orphaned single-PUT objects. Lifecycle counters are
exported as `filehub_direct_uploads_total{mode,outcome}`. Cleanup failures retain
the session for a leased retry and emit `outcome="orphan_cleanup_failed"`; a
session is deleted and counted as `orphan_cleaned` only after every provider and
chunk cleanup succeeds. Asset processing uses a database claim so completion
retries cannot enqueue the same asset twice.

`DELETE /uploads/{uploadId}` cancels an unfinished session and removes a
single-part direct object when one was already uploaded. Direct upload URLs are
short-lived (up to 15 minutes); this is independent from the 7-day signed
download URL lifetime. Request fields accept both camelCase and snake_case.
Local filesystem storage continues to use `POST|PUT /assets` because it cannot
issue a provider-native upload URL.

## Workspace Sync

FileHub can act as the sync backend for shared Saker workspaces. Enable it with
`FILEHUB_WORKSPACES_ENABLED=true`. A workspace is a tenant-scoped directory of
versioned paths; every path revision references an immutable FileHub asset, so
file bytes stay in the existing asset data plane while the workspace tracks
paths, versions, and change history.

Workspaces and sync:

```bash
# Create a workspace
curl -X POST http://127.0.0.1:17040/v1/workspaces \
  -H 'Authorization: Bearer <key>' -H 'Content-Type: application/json' \
  -d '{"name":"team-sync"}'

# List / inspect / soft-delete
curl http://127.0.0.1:17040/v1/workspaces
curl http://127.0.0.1:17040/v1/workspaces/<id>
curl -X DELETE http://127.0.0.1:17040/v1/workspaces/<id>

# Browse current tree, one entry, history, and incremental changes
curl 'http://127.0.0.1:17040/v1/workspaces/<id>/tree?prefix=docs/'
curl 'http://127.0.0.1:17040/v1/workspaces/<id>/entries?path=docs/report.md'
curl 'http://127.0.0.1:17040/v1/workspaces/<id>/history?path=docs/report.md'
curl 'http://127.0.0.1:17040/v1/workspaces/<id>/changes?after=0'
```

Atomic commit applies many `put`/`delete` operations in one transaction and is
idempotent per `Idempotency-Key`. Every operation carries the client-visible
`base_revision_id`; a stale base redirects a `put` to a deterministic
`*.saker-conflict-<device8>-<request8>-<ext>` path and keeps the remote version
for a stale `delete`:

```bash
curl -X POST http://127.0.0.1:17040/v1/workspaces/<id>/commits \
  -H 'Authorization: Bearer <key>' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: 7f1c…' \
  -d '{"device_id":"dev-1","session_id":"sess-1","operations":[
        {"kind":"put","path":"docs/report.md","asset_id":"asset-…","base_revision_id":"wrev-…"}]}'
```

Referenced assets are protected: an asset referenced by any workspace revision
cannot be removed by single delete, bulk delete, or expiry GC (those paths skip
it), and committing a reference clears the asset's expiry eligibility.

Public sharing and read stats:

```bash
curl -X POST http://127.0.0.1:17040/v1/workspaces/<id>/shares \
  -d '{"path":"docs/report.md","expires_in":"24h"}'   # token returned once
curl http://127.0.0.1:17040/s/<token>                 # anonymous, sandboxed
curl -X POST http://127.0.0.1:17040/v1/workspaces/<id>/read-events \
  -d '{"events":[{"path":"docs/report.md","kind":"agent","count":1}]}'
curl 'http://127.0.0.1:17040/v1/workspaces/<id>/read-stats?days=30'
```

The workspace API is documented in the OpenAPI schema at
`/v1/openapi.json`. Metrics are exported as `filehub_workspace_*` counters.

## Web UI

The web UI is built into `web/static` by `make build` or `npm run build` under `web/`. Heavy preview dependencies, including model viewer, audio waveforms, markdown rendering, and syntax highlighting, are loaded only when an asset preview needs them. When Workspace Sync is enabled, a Workspaces page lists workspaces and lets you browse the tree, history, shares, and read stats.
