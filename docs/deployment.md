# Local deployment

Open Maps runs as one Go service with independently selected lookup, routing and
basemap artifacts. Places/geocoding uses an immutable generation directory:
normalized Parquet is authoritative and `serving.duckdb` is a derived lookup
catalog. There is no compact-SQLite runtime or automatic fallback.

## Native build requirements

DuckDB requires CGO and a native C/C++ toolchain. Build on the deployment
platform (or use a correctly configured CGO cross-toolchain); `CGO_ENABLED=0`
is unsupported. CI builds and smoke-tests the Linux deployment binaries with
CGO enabled. DuckDB itself is linked into the executable and no DuckDB extension
files are deployed.

Build a new executable rather than replacing one used by a running process:

```sh
CGO_ENABLED=1 go build -o data/openmaps-server-next ./cmd/server

data/openmaps-server-next \
  -lookup-selection data/lookup-selection.json \
  -routing-snapshot data/scout-prepared \
  -routing-selection data/routing-selection.json \
  -tiles data/newport.pmtiles \
  -public public \
  -listen 127.0.0.1:8080

curl -fsS http://127.0.0.1:8080/healthz
```

`-lookup-selection` is the default. `-lookup GENERATION -lookup-selection ''`
opens one verified generation directly for smoke testing. A missing, corrupt or
incompatible lookup artifact fails startup; it never selects SQLite.
`-routing-selection` independently watches a JSON document containing
`{"directory":"prepared-directory"}`. The server defaults to four concurrent
routing requests, four read-only DuckDB connections, one DuckDB thread per
connection, and a 128 MiB routing graph-page cache per reader.

Health reports the lookup manifest reference and last rejected lookup reload,
plus the independent routing identity. Lookup responses carry
`X-OpenMaps-Lookup-Snapshot`.

## Activation and rollback

Build, compare and review a new lookup generation as described in
[the refresh guide](refresh.md). Activation changes only the synced selection
document. The live server verifies and opens the new generation before swapping
it into service; a failed reload leaves the old generation active and degrades
health with the exact error. Request leases prevent the old generation from
closing while an API or routing-coordinate-resolution request still uses it.

Rollback selects the previously verified generation:

```sh
go run ./cmd/places-geocoding-refresh rollback \
  -selection data/lookup-selection.json
```

Never modify or remove a selected generation. Retain at least the current and
previous directories and their manifests. A rollback does not rebuild an index
or copy artifact bytes.

## Graceful shutdown

SIGTERM drains HTTP requests for up to 35 seconds, then closes lookup and routing
readers. `cmd/scout-run-bounded` can supervise a compiled server and record RSS
and free disk, but it does not install a service manager. Remote exposure,
authentication, automatic failure recovery and system-wide startup are outside
this repository's current deployment scope.

The September 2026 launchd paths and ports remain historical evidence in the
[Scout cutover log](log/0036-scout-deployment-cutover.md); they are not reusable
configuration.
