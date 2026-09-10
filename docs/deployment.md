# Local Go deployment

Two loopback instances run the same unified `cmd/server` binary under the Go
resource supervisor and per-user macOS launchd jobs:

| Instance | URL | Routing snapshot | launchd label |
| --- | --- | --- | --- |
| National | `http://127.0.0.1:8097` | `data/scout-national-20260909/national-aleutian-prepared` | `com.openmaps.national` |
| Regional | `http://127.0.0.1:8096` | `data/scout-national-20260909/regional-prepared` | `com.openmaps.regional` |

Both use `data/deployment.json` for Places/geocoding, `data/newport.pmtiles` for
basemap ranges and `public/` for the map UI. National routing also watches the
existing `data/scout-national-20260909/national-final-selection.json`. These
lookup and routing selection files remain independent.

The current release lives in `data/scout-cutover-20260909/`: compiled binaries in
`bin/`, launchd configuration copies in `launchd/`, append-only service logs in
`national.log` / `regional.log`, and per-lifetime resource reports in
`reports/national/` / `reports/regional/`. Reports are completed on process exit;
an empty active report is expected. Source and binary pins and verification
results are in the [historical cutover record](log/0036-scout-deployment-cutover.md).

LaunchAgents are installed in `~/Library/LaunchAgents/`. They start at login and
survive the terminal or coding session. They deliberately do not automatically
retry failures: investigate an exit or resource-budget crossing before restarting.
Each supervisor samples its server's RSS against 4 GiB and requires 32 GiB free
disk. These are sampled per-process limits, not total-machine memory limits.
The runtime executable search path contains only `ps`; no Python is used.

## Inspect, stop and restart

```sh
launchctl print "gui/$(id -u)/com.openmaps.national"
launchctl print "gui/$(id -u)/com.openmaps.regional"
curl -fsS http://127.0.0.1:8097/healthz
curl -fsS http://127.0.0.1:8096/healthz
```

Healthy responses include lookup availability, the lookup dataset checksum,
routing profile, snapshot identity and reload diagnostics. The established
`routing_candidate` JSON key remains for compatibility; internal Go ownership is
`routing.Service`.

For a graceful planned stop, identify the server child of the supervisor PID
shown by `launchctl print`, confirm its full command, and send **that server**
SIGTERM. It drains HTTP requests for up to 35 seconds. Wait for it and its
supervisor to exit, then inspect the completed resource report. Signalling the
supervisor instead uses the resource-abort termination path, with a five-second
grace period.

Restart an exited, still-loaded job:

```sh
launchctl kickstart "gui/$(id -u)/com.openmaps.national"
```

Use `com.openmaps.regional` for the other instance. Avoid `kickstart -k` during
traffic because it interrupts the current process. To unload an exited job, use
`launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.openmaps.national.plist`.
To load it again, replace `bootout` with `bootstrap`, then use `kickstart`
for an immediate start; launchd may defer the initial run-at-load request.

## Update and rollback

Build into a new release directory. Verify its binaries against the frozen
national cases, full HTTP responses, lookup suites, basemap byte ranges and
snapshot replacement on isolated ports before changing the live ports. Retain
the previous binaries, launch configuration, resource reports and selection
files. Never rewrite prepared graph payloads or an actively executing binary.

For the cutover release, rollback launch commands are retained in
`data/scout-cutover-20260909/rollback/national.sh` and `regional.sh`. After stopping
and unloading the corresponding new job, run the selected script with `/bin/sh`.
It starts the original binary on its original port under the **Go** supervisor,
with a fresh report. Those old binaries restore routing-only behavior and should
be temporary rollback tools. The scripts do not restore or overwrite selection
files; baseline copies are retained beside them for inspection if needed.

This is a local user-session deployment. System-wide startup before login,
automatic failure recovery, remote exposure and production orchestration are
not configured here.
