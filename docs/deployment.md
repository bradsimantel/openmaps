# Local deployment

Open Maps can run one or more loopback instances of the unified `cmd/server`
binary. Each instance needs explicit paths for its Places/geocoding selection,
routing snapshot or selection, basemap archive and public files. The repository
does not assert that any service is currently running and does not contain an
installed launchd configuration.

The September 2026 ports, launchd labels, binaries, rollback scripts and retained
release directories are historical deployment evidence, recorded in the
[Scout cutover log](log/0036-scout-deployment-cutover.md). Do not treat those
machine-local paths as a reusable configuration.

## Start and inspect an instance

Build a new executable rather than replacing one used by a running process:

```sh
go build -o data/openmaps-server-next ./cmd/server

data/openmaps-server-next \
  -deployment data/deployment.json \
  -routing-snapshot data/scout-prepared \
  -routing-selection data/routing-selection.json \
  -tiles data/newport.pmtiles \
  -public public \
  -listen 127.0.0.1:8080

curl -fsS http://127.0.0.1:8080/healthz
```

`-deployment` selects Places/geocoding SQLite snapshots.
`-routing-selection` independently watches a JSON document containing
`{"directory":"prepared-directory"}`; relative directories resolve beside that
document. `-routing-snapshot` supplies the verified initial routing snapshot.
The server defaults to four concurrent routing requests and a 128 MiB graph-page
cache per reader. Use `-routing-concurrency` and `-routing-cache-mib` to change
those bounded settings.

Healthy responses report `lookup_snapshot` for the Places/geocoding selection and
`routing_candidate` for the routing snapshot. `routing_candidate` remains the
established compatibility name. Lookup responses carry
`X-OpenMaps-Lookup-Snapshot`.

## Supervision and graceful shutdown

`cmd/scout-run-bounded` can supervise a compiled server, sample its RSS and free
disk, and write a distinct resource report for each process lifetime. It does not
install or manage launchd jobs. If launchd or another process manager is used,
keep its configuration outside generated data and record the exact executable,
arguments, resource limits and restart policy.

For a graceful planned stop, signal the server process with SIGTERM. It drains
HTTP requests for up to 35 seconds. Signalling the resource supervisor instead
uses its resource-abort path and a five-second grace period. Avoid forced process
replacement during traffic.

## Update and rollback

Build into a new release path. Before changing a service definition or routing
selection, verify its binaries against the maintained national cases, full HTTP
responses, lookup suites, basemap byte ranges and snapshot replacement on an
unused loopback port. Never rewrite prepared graph payloads, selected SQLite
files or an executing binary.

Retain the previous executable, service definition, lookup and routing selection
documents, prepared snapshots and completed resource reports. Rollback should
restore those explicit retained inputs; it must not depend on a dated path from a
historical log. Confirm health and both snapshot identities after every update or
rollback.

This is a local user-session deployment. System-wide startup before login,
automatic failure recovery, remote exposure and production orchestration are not
configured by this repository.
