# AssetHub

AssetHub is a local asset service with a Go API, embedded web UI, metadata search, signed download URLs, and local/S3/OSS storage backends.

## Run

Create `.env` when local overrides are needed, then run:

```bash
make run
```

`make run` automatically includes `.env`, builds the frontend and backend, creates runtime directories, and starts `./assethub`. It writes the running PID to `.synapse/stack/assethub.pid`.

Before starting, `make run` checks the configured listen port from `ASSETHUB_ADDR`. It stops a previous AssetHub process recorded in the PID file or already listening on the port. If the port is owned by another process, it fails instead of killing an unrelated service.

## Configuration

Common environment variables:

```bash
ASSETHUB_ADDR=:17040
ASSETHUB_DSN=sqlite://.synapse/stack/assethub.db
ASSETHUB_STORAGE_BACKEND=osfs
ASSETHUB_STORAGE_DIR=.synapse/stack/assethub-data
ASSETHUB_API_KEY_AUTH_ENABLED=false
ASSETHUB_API_KEYS=
ASSETHUB_PRESIGN_SECRET=change-me
ASSETHUB_PID_FILE=.synapse/stack/assethub.pid
```

OSS uses the `ASSETHUB_OSS_*` names:

```bash
ASSETHUB_STORAGE_BACKEND=oss
ASSETHUB_OSS_ENDPOINT=https://oss-cn-shanghai.aliyuncs.com
ASSETHUB_OSS_REGION=cn-shanghai
ASSETHUB_OSS_BUCKET=your-bucket
ASSETHUB_OSS_ACCESS_KEY=your-access-key
ASSETHUB_OSS_SECRET_KEY=your-secret-key
```

S3 uses `ASSETHUB_S3_*` names with the same shape:

```bash
ASSETHUB_STORAGE_BACKEND=s3
ASSETHUB_S3_ENDPOINT=http://127.0.0.1:9000
ASSETHUB_S3_REGION=us-east-1
ASSETHUB_S3_BUCKET=assethub
ASSETHUB_S3_ACCESS_KEY=your-access-key
ASSETHUB_S3_SECRET_KEY=your-secret-key
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

Set a non-default `ASSETHUB_PRESIGN_SECRET` before exposing fallback signed URLs outside a trusted local environment.

## Web UI

The web UI is built into `web/static` by `make build` or `npm run build` under `web/`. Heavy preview dependencies, including model viewer, audio waveforms, markdown rendering, and syntax highlighting, are loaded only when an asset preview needs them.
