# Routing package and acquisition handoff cleanup

Date: 2026-09-09 (America/Los_Angeles). Scope: package organization and preparation
handoff after `c5601b3`; verification precedes any subsequent commit. Historical
implementation record. Current architecture and commands are maintained in
[Scout routing](../routing-scout.md).

## Package ownership

Moved `internal/routing/valhallatiles` into `internal/routing`, including its
source decoder, search, graph/index preparation, landmarks, service/snapshot
ownership, independent path verifier, tests and small deterministic fixtures.
There is no compatibility forwarding package. Command/API imports and fixture
paths use the routing domain name. The package move does not alter the persisted
graph/index formats, snapshot fingerprint domain, profile or HTTP metadata.

`routing/qualification` stays separate: the coverage and full HTTP verification
commands use it, while the production server does not. `importer/scout` owns
provider acquisition metadata and receipts; `importer/addressdata` continues to
decode retained source address components for geocoding. Lookup importers and
shared OSM/PBF street parsing are unchanged. Historical reports and their pinned
source paths remain historical rather than being rewritten to describe new code.

## One typed preparation handoff

`scout.ReadAcquiredPlan` reads the bounded plan once, validates the complete local
manifest, catalog omissions, selected regions/dependencies, generation and
budgets, then checks every package receipt. It returns typed inputs containing
receipt SHA-256 values and optional dataset/tile pins. It performs no network
requests or input writes. The preparation command explicitly converts those
inputs into a routing lock instead of decoding the same plan into a second
anonymous struct and repeating its metadata checks.

Receipt validation is shared with fetch/resume and offline acquisition
verification. Receipt ID, byte count, MD5, SHA-256, canonical source URL and
metadata generation must agree. Plan-only and receipt-only tile constraints are
retained; when both specify tile lists, differing lists are rejected. The old
command replaced plan packages with receipt packages and could lose a tile pin
present only in the plan. Regression tests cover this case.

This validates metadata and constraints, not the actual road bytes. Graph
preparation still hashes compressed packages, checks their timestamps/dataset,
verifies every explicit tile pin and decoded member, rejects corruption and
conflicting duplicates, enforces incremental disk limits and publishes immutable
receipts only after success. Graph and landmark extension checks remain intact.

## Verification

Artifacts are retained under `data/scout-package-cleanup-20260909/`.

- All **37 moved files**, including binary fixtures, match their previous contents
  after only package names, fixture paths and the package comment are adjusted.
- `gofmt`, `go test ./...`, `go vet ./...` and `git diff --check` pass. Race checks
  pass for preparation, server, API, routing, qualification and Scout acquisition.
  The production server dependency list excludes `routing/qualification`.
- New deterministic tests preserve plan-only, receipt-only and matching tile pins;
  reject receipt/generation/checksum/URL/identity conflicts, budget escalation,
  missing receipts and cancellation; and verify that actual graph preparation
  rejects damaged package/tile pins, duplicate/missing members and mixed source
  generations without publishing a receipt.
- Downloaded-input checks rehash all **647 national packages** against retained
  receipts and verify the new typed handoff against the independent repository
  qualification lock. No network or dataset mutation is involved.
- A fresh **14-package regional graph** builds through the simplified command in
  **29.218 s**, with **49,152,000 bytes** sampled peak RSS. Its payload hash, byte
  count, tile index, source generation and package pins match the retained regional
  graph. New forward turns, potential and reverse-support indexes publish, and two
  directed landmark pairs build in **9.605 s**, at **476,938,240 bytes** sampled RSS.
  Separate route/HTTP integration checks pass on the fresh graph and landmarks.
- Real Scout boundary, spatial-index, source inventory and simple/complex turn
  restriction tests pass from the relocated package. The two explicit seed-discovery
  experiments remain opt-in and are skipped.
- National offline verification passes **83/83**: **75** independently verified
  routes, **66** ordinary-reference matches, **2** unreachable and **6** unsnappable.
  It takes **77.021 s** with **781,025,280 bytes** sampled peak RSS.
- Full HTTP verification against the newly compiled isolated server passes
  **83/83** in **17.656 s**, receiving **5,198,292 bytes** with zero mismatches and
  the unchanged national snapshot ID
  `453d3ad01d124f750fb80220d237c00da3ec5b0e939c64545e7c78cf09f3cc27`.
  The isolated server exits **0** after verification; its sampled peak RSS is
  **1,588,314,112 bytes**.

Supervised preparation, landmark construction, offline checks and serving use an
executable search path containing only `ps`; Python is not involved. Verification
uses a 33 GiB disk reserve, above the live deployments' 32 GiB threshold. Timings
come from a shared host with overlapping jobs and are not cold-disk benchmarks.
The fresh graph has intentionally different resource-budget fields in its receipt;
its unchanged payload and source pins establish equivalence, not an identical
receipt hash.

The existing deployment on 8096/8097 is not replaced or restarted. Both final
health responses match their pre-work copies byte-for-byte. The isolated port
8106 is stopped, downloaded datasets and prior artifacts are retained, and these
changes are not committed or pushed during implementation. Source-data defects,
coverage limits, the supported profile and unavailable address/ferry routing remain
unchanged. The [machine-readable evidence](0037-routing-package-and-acquisition-handoff.json)
pins the new binaries and completed verification artifacts.
