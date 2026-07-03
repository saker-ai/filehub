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

OSS uses the `FILEHUB_OSS_*` names:

```bash
FILEHUB_STORAGE_BACKEND=oss
FILEHUB_OSS_ENDPOINT=https://oss-cn-shanghai.aliyuncs.com
FILEHUB_OSS_REGION=cn-shanghai
FILEHUB_OSS_BUCKET=your-bucket
FILEHUB_OSS_ACCESS_KEY=your-access-key
FILEHUB_OSS_SECRET_KEY=your-secret-key
```

S3 uses `FILEHUB_S3_*` names with the same shape:

```bash
FILEHUB_STORAGE_BACKEND=s3
FILEHUB_S3_ENDPOINT=http://127.0.0.1:9000
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

The default signed URL TTL is `7d` (`168h`). Request a shorter TTL with:

```bash
curl -X POST 'http://127.0.0.1:17040/v1/assets/{id}/presign' \
  -H 'Content-Type: application/json' \
  -d '{"expires_in":"5m"}'
```

Set a non-default `FILEHUB_PRESIGN_SECRET` before exposing fallback signed URLs outside a trusted local environment.

## Web UI

The web UI is built into `web/static` by `make build` or `npm run build` under `web/`. Heavy preview dependencies, including model viewer, audio waveforms, markdown rendering, and syntax highlighting, are loaded only when an asset preview needs them.
