# Go deployment cutover and remaining Scout cleanup

Date: 2026-09-09 (America/Los_Angeles). Scope: follow-up to migration commit
`8d4a688`, with this cutover's code and documentation changes initially uncommitted.
Historical implementation and verification record. Current service management is
maintained in [local deployment](../deployment.md) and [Scout routing](../routing-scout.md).

## Code and compatibility

Renamed routing ownership from `Candidate` to `Service`, its metadata to
`ServiceMetadata`, its lease to `Lease`, and its profile constant to `Profile`.
The public profile and cost model remain unchanged. The versioned
`scout-candidate-v2` fingerprint domain and `/healthz`'s `routing_candidate` key
remain intentionally compatible with retained snapshot identities and monitors.
Lookup refresh candidates and road-snapping candidates are separate concepts
and retain their names.

Removed the offline bidirectional query implementation, its audit/verification
flags, reverse prohibition-trie preparation and loading, exclusive reader/cache
ownership, and `-reverse-turns-only`. The useful partial-edge adversaries now
compare the serving A* search with ordinary Dijkstra. Tests for access/turn
restrictions, nonreciprocal opposing edges, partial endpoints, forward turn
corruption, landmarks, snapshot leases and independent source-path replay remain.
Forward preparation keeps its existing format. Reverse graph enumeration and
reverse-support certificates remain necessary for directed landmark preparation.
Existing reverse-turn payload files in downloaded graphs remain untouched;
current tooling no longer generates or loads them.

The Go supervisor gains `-report-dir`, exclusive with `-report`, to allocate a
unique resource report on every service start. A restart cannot overwrite an
older report or fail merely because a previous lifetime finished. Tests cover
separate reports and rejection of conflicting destinations.

The historical migration record now identifies commit `8d4a688` and distinguishes
its pre-commit verification from this later deployment change. Maintained commands
no longer offer the retired offline experiment.

## Deployment

The release artifacts are isolated in `data/scout-cutover-20260909/`. Both existing
loopback ports are assigned the same compiled unified server: national 8097 and
regional 8096. Both use the existing `data/deployment.json`, lookup SQLite,
`public/` and Newport PMTiles. National routing retains its existing selection
file and expanded Aleutian snapshot; regional routing retains its original graph.
No source package, graph, landmark, database or original binary is overwritten.

Per-user launchd jobs `com.openmaps.national` and `com.openmaps.regional` run the
Go supervisor with 4 GiB sampled server RSS and 32 GiB free-disk thresholds.
They start at login and remain independent of the terminal/coding session.
Automatic failure retries are disabled to avoid repeated starts following a
budget abort. This is local user-session hosting, not system-wide production
orchestration. The executable search path contains only the system `ps` command.

Original binaries, baseline health/selection evidence and explicit rollback
commands are retained. Rollback scripts use the old binaries under the **Go**
supervisor, so rollback need not restore the deleted Python supervisor script.
They restore the earlier routing-only behavior. The ignored historical Python
files and original graph auxiliary files remain archival data, not runtime dependencies.

## Verification

- `gofmt`, `go test ./...`, `go vet ./...` and `git diff --check` pass.
  Race checks pass for the server, API, lookup deployment, Scout and supervisor.
  Acquisition code is unchanged from the prior migration.
- Separate downloaded regional graph/landmark and HTTP integration tests pass.
  The retained partial-edge adversary exercises 1,200 deterministic cases across
  simple/complex-turn combinations against ordinary Dijkstra. Landmark partials,
  corruption, source opposing-edge identity and snapshot lease tests also pass.
- Final-binary national offline verification passes **83/83**: **75** independently
  verified routes, **66** ordinary-reference matches, **2** unreachable and **6**
  unsnappable. It takes 82.796 s and samples 783,040,512 RSS bytes. Startup hashing
  overlaps other verification on this shared host; this is not a cold-disk benchmark.
- Isolated national HTTP verification passes **83/83** full bodies in 16.766 s,
  receiving 5,198,292 bytes. Live 8097 verification passes **83/83** in 21.785 s
  with the same byte count, zero mismatches and national snapshot
  `453d3ad01d124f750fb80220d237c00da3ec5b0e939c64545e7c78cf09f3cc27` unchanged.
- Isolated two-snapshot replacement passes **801/801** full responses,
  **608,333,796 bytes**, both snapshots observed, and zero errors. Admission yields
  **2 HTTP 200 / 6 HTTP 429** for eight simultaneous clients; cancellation releases
  capacity. Malformed/nonexistent selections retain the serving snapshot and
  distinct diagnostics; the original selection is restored.
- Live Places/geocoding suites pass on both isolated ports and both deployed ports.
  Basemap requests return **HTTP 206**, the expected 127-byte range and PMTiles
  header. Regional Newport full geometry/cost matches the independently verified
  national case before cutover, after cutover, and after the managed restart.
- Staged national service peak sampled RSS is **2,019,049,472 bytes** during all
  validation, below its 4 GiB threshold; it and the staged regional service exit
  **0** on graceful shutdown. The isolated services on 8106/8107 are stopped.
- Regional launchd restart is exercised: the server drains and both server and
  supervisor exit **0**. `kickstart` starts a new lifetime with unchanged health
  identity, retains the completed report, and allocates a separate report file.
  Both deployed jobs remain running. The previous Python supervisor and both
  original routing-only processes have exited.
- Lookup and national routing selection files compare byte-for-byte with their
  pre-cutover copies. Existing graphs, packages and rollback binaries are retained.
  The older regional binary used an earlier snapshot fingerprint format; its
  graph is unchanged but the unified service reports the current fingerprint
  `a95fc11c45adee7793394ef9e76d388dd7fea7464cc602098d1e5a9da1a2a7dd`.

The initial admission test overlapped the full HTTP checker and saw only one
available slot. Its isolated repeat above passes; the earlier log is retained.
An initial compiled lookup-test invocation used the repository root instead of
its package working directory and could not locate fixtures; corrected invocations
pass. Launchd deferred the regional run-at-load request until explicit `kickstart`;
initial connection failures are retained separately from successful live checks.

The [machine-readable evidence](0036-scout-deployment-cutover.json) pins final source
files, binaries, launch configurations and completed reports. Runtime reports for
still-running jobs are intentionally unfinished until shutdown. Verification and deployment preceded the
subsequent user-authorized commit and push. The 19 source geometry discrepancies, 706 absent tile
references, unsupported address/ferry routing and other profile limitations from
[the historical qualification](0034-national-scout-qualification.md) remain unchanged.
