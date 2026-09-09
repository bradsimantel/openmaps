# Prepared routing snapshots

Routing snapshots can be prepared offline and loaded directly by the single Go
service. The prepared loader does not decode source graph chunks, reconstruct
adjacency, build spatial indexes, or run hierarchy preprocessing. The Google API,
endpoint policy, graph/profile versions and `estimated-driving-v1` costs are
unchanged. Nationwide construction and physical residency remain separate gates.

## Prepare and serve

Use new output names under ignored `data/`. Keep the SQLite snapshot immutable.
For example, using the retained multistate candidate:

```sh
go run ./cmd/routing-prepare \
  -db data/recursive-scale/northwest-candidate.sqlite \
  -out data/prepared-routing

go run ./cmd/server \
  -db data/recursive-scale/northwest-candidate.sqlite \
  -routing-prepared data/prepared-routing \
  -listen 127.0.0.1:8089
```

Preparation validates the entire snapshot, including lookup integrity, identity
and provenance, and constructs canonical routing data from SQLite. It then writes
an immutable artifact and a separate publication receipt. It refuses an existing
receipt or artifact; independent preparations use another directory. It does not
modify SQLite or activate a deployment. Preserve receipts with the source locks,
SQLite files and review records.

`-routing-prepared` also works with `-deployment`. Prepare every routing-enabled
snapshot that the deployment may select, including rollback snapshots, into that
directory. Lookup-only snapshots retain their existing validation and remain
supported without a routing receipt. For rollback without graph reconstruction:

```sh
go run ./cmd/refresh rollback \
  -state data/isolated-deployment.json \
  -routing-prepared data/prepared-routing
```

The example state path must refer to an explicitly initialized deployment.
Preparation and measurement do not authorize changes to the active deployment.

Production server startup requires preparation for a routing-enabled snapshot.
Missing, foreign, damaged or unsupported artifacts fail loading; they do not
trigger reconstruction. For an explicit legacy investigation, use
`-routing-legacy-load`, optionally with `-routing-cache`. The latter retains the
older numeric-only mapping backend and its full construction-memory cost. The
prepared and legacy options cannot be combined. Library `routing.Open`,
`routing.Load`, `routing.OpenMapped` and dataset `Open` retain their explicit
legacy/offline behavior for import, inspection and compatibility tests.

## Representation and resident inventory

New publications use **`routing-prepared-le64-v2`**. The reader also supports
retained **`routing-prepared-le64-v1`** publications and explicitly checks that
receipt and artifact versions agree. Each version has its own artifact filename;
preparing the same SQLite snapshot again requires a new publication directory.
One directory may contain either version for different snapshot digests, including
rollback snapshots. Neither existing artifacts nor SQLite snapshots are migrated.

Both versions use a 4 KiB header and ordered aligned sections.
Integers have explicit little-endian widths; coordinates and costs retain their
float64 bits. It uses the checked 64-bit little-endian Linux/macOS mapping ABI.
Unknown versions and unsupported ABIs fail. No native Go pointer representation
is stored. Section dimensions are checked against actual file size before views
are created, with overflow-safe extent checks and canonical zero padding.

| Runtime structure | Prepared representation / loading |
| --- | --- |
| Coordinates, CSR offsets and adjacency | Mapped numeric arrays; adjacency retains directed-edge ordinals |
| Directed edges | v2: 32-byte records with dense node endpoints; v1: original 48-byte records with source node endpoints |
| Segment directions, destination zones, weak components | Mapped arrays |
| Forced continuations, junction selection, recursive cells | Mapped bounds, entrance ranges, transfers and parent-before-child source-path records |
| Segments and source references | Fixed 48-byte records; source ID text in a mapped string pool; returned strings are copied |
| Excluded-road snap guards | Fixed 56-byte records and the same string pool |
| Source-node lookup, public/restriction-node membership | Mapped 16-byte hash slots; stable mixing, source-ordered insertion, power-of-two capacity and at most 50% occupancy |
| Road, guard, area and driveway spatial indexes | Mapped 48-byte tree nodes and int32 feature ordinals; no runtime tree construction |
| Restriction automaton | Persisted transitions, failure links and banned flags, decoded into resident maps |
| Address association | Persisted names, areas, entrances, geometry and driveway adjacency, decoded into resident Go data |
| Metadata and maximum heuristic speed | Persisted, checked against publication metadata |
| Provenance, source tags, cost assumptions | Remain authoritative in SQLite; validated during preparation, covered by whole-file runtime integrity |
| Places/geocoding | Existing SQLite lookup and resident address-index behavior; not converted by this milestone |
| Per-request search labels/queue and output geometry | Existing request allocations, limited in concurrency by HTTP admission |

Prepared v2 changes only the directed-edge section. Its fields are two uint32
zero-based node ordinals (offsets 0 and 4), a uint32 segment ordinal in the low
31 bits with reverse direction in bit 31 (offset 8), an int32 cell-entry index
(offset 12), and the original float64 metres and seconds (offsets 16 and 24).
Node, directed-edge and segment counts are limited to `MaxInt32`; out-of-range
references and incompatible dimensions are rejected. This encoding keeps the
existing coordinate/CSR order and edge identities. The writer streams records
from the canonical source-validated graph; the runtime maps them directly without
building a translation map or reconstructing source edges.

Search translates its selected endpoints once and uses dense keys for coordinate,
adjacency and junction access, including exact geometry reconstruction. These keys
are internal to one immutable publication. Source segment endpoints, snap node
identities, public lookup IDs, address evidence and driveway relationships remain
source-based. The bounded endpoint walk obtains original directed endpoints from
source segments. Snapping and source/access membership checks still use the
source-node hash table; it is retained unchanged. Smaller segment/adjacency records
and eliminating remaining endpoint lookups are separate possible experiments.
The restriction automaton, cell hierarchy, search labels, cost model, tolerances
and endpoint policy are unchanged.

Ancillary JSON contains the restriction automaton, address evidence, driveway
adjacency and metadata. Its disk size is capped at **128 MiB**. Oversized snapshots
fail preparation/loading and require a new representation; they do not silently
rebuild. JSON decoding has allocation overhead beyond its byte size. This cap is
an explicit limit on supported ancillary input, not a 128 MiB Go-heap guarantee.
The retained multistate ancillary section is small because it has no address
coverage. National address association will require further mapped or bounded
access representations. The persisted structures are decoded, not recomputed.

Runtime source hashing uses a 1 MiB buffer. Artifact hashing scans the read-only
mapping in 1 MiB steps. Structural validation uses constant scan scratch rather
than graph-sized maps or arrays. Ancillary decoding and existing lookup indexes
are the remaining resident allocations. Query memory is a separate budget.

## Preparation, publication and runtime trust

A checksum inside an artifact proves only self-consistency. It cannot certify
that a shortcut follows authoritative roads or is cheapest under the cost model.
The reader therefore requires a **separate trusted publication receipt**, selected
by the SHA-256 of the complete SQLite snapshot. The receipt binds that snapshot,
source graph digest, metadata, prepared version and the SHA-256 of the complete
artifact, including its header. The artifact fixes `junction-cells-v1` and its
numeric field layout; source metadata fixes graph, profile and cost versions.

The trusted publisher is `cmd/routing-prepare`, run against approved local source
snapshots. It takes the source digest before complete importer validation and
checks it again before publication. It constructs the canonical arrays itself;
there is no operation to approve a caller-supplied artifact. The graph loader
retains semantic/provenance validation, restrictions, directional costs and access
evidence checks. Preparation writes and syncs a temporary file, links it without
replacement, and publishes the receipt last with directory synchronization. An
interruption may leave an unreferenced artifact; it cannot leave a successful
receipt pointing to partially written data. Use a new directory to retry.

The receipt directory is trusted operator configuration, comparable to deployment
selection, **not an authentication or signature system**. Do not populate it with
receipts delivered alongside untrusted artifacts. Anyone able to replace both a
trusted receipt and its artifact can change the publication authority. Receipt
self-consistency is not a substitute for running the trusted preparation command.
The Go publication method requires a source-validated store and a source digest
taken before complete snapshot validation; callers must honor that boundary.

Runtime verifies the entire SQLite file and complete artifact against the receipt.
It checks versions, dimensions, references, CSR ranges, node probing, spatial
children/ranges, recursive path ordering, restriction transitions/failure cycles,
access geometry dimensions and finite numeric values. It does not recompute
shortest cell paths, source tag interpretation or the complete graph. Those
semantic obligations belong to preparation and the independently pinned digest.
Changing an artifact and its own payload checksum cannot authorize new topology.
Dense endpoints are checked against both their coordinate/CSR bounds and the
source-node ordinals of the referenced segment, in the recorded direction.

Artifacts and SQLite files must remain immutable while in use. Read leases cover
search and reconstruction; HTTP deployment leases extend through encoding. Close
waits for readers, unmaps once, and rejects later queries. Failed unpublished
loads release mappings. Failed replacement preserves the prior handler and marks
health degraded. Prepared rollback streams the selected SQLite file and complete artifact against
that receipt before changing selection, using a 1 MiB buffer and no query mapping.
It checks the selected source digest, receipt format, graph/profile/cost metadata
and publication binding. This verifies equality to an already canonical trusted
publication; it does not approve a new caller-supplied artifact. Deep structural
checks remain at offline preparation and runtime loading before handler publication.
Lookup-only rollback still performs complete lookup validation. Cancellation or a
failed publication check leaves the selected state and serving mapping unchanged.

## Measurement and remaining gates

The [historical prepared-loading evaluation](log/0022-directly-loadable-routing-snapshots.md)
records phase measurements, predeclared budgets, all three regional workloads and
lifecycle verification. These are desktop file-cache observations. Integrity
validation reads every page, so subsequent first requests are not cold-disk tests.
`MADV_DONTNEED` is only a residency hint; it does not establish a cold system cache.
A controlled cold-cache host experiment remains unperformed. The subsequent
[historical residency investigation](log/0023-routing-residency-and-rollback.md)
distinguishes duplicate mapping aliases from clean file residency and replaces
rollback's temporary query mapping with streaming publication verification.
The [historical dense-edge experiment](log/0024-dense-routing-edges.md) records the
v2 representation budgets, source checks and page/residency measurements. It
separates record-page accesses from physical residency and normal HTTP timing.

The mapped virtual size, process RSS and macOS physical footprint describe
different resources. Mapping does not impose a hard physical-memory bound, and
old/new snapshot mappings can overlap. The current reader also scans all mapped
bytes for integrity at load time. National artifact size, cold storage bandwidth,
residency under pressure and the 128 MiB ancillary cap remain readiness gates.

Offline preparation still constructs the full regional graph and hierarchy and
can allocate tens of GiB cumulatively. Importing the national source graph is not
bounded by this runtime loader. External-memory import/preprocessing remains
separate work. No national data, new address data, cost-model correction, deeper
routing hierarchy or browser change is part of this milestone.

The opt-in source/reference harness accepts `OPENMAPS_REFERENCE_TIMEOUT` from
30 seconds through five minutes (default 30 seconds), logging the override for
each case. This is only an offline correctness-check budget. It does not change
HTTP timeouts, cancellation, search results or numerical tolerance. The retained
exhaustive multistate reference run required a larger allowance under desktop
memory pressure; that observation is distinct from the normal HTTP workload.

## Reproduce residency observations

`scripts/routing-residency.py` runs one command at a time and records macOS
`proc_pid_rusage` v2 samples: process RSS, charged footprint, pageins and storage
read/write bytes. It also captures read-only host VM, swap and memory-pressure
queries before and after the run. It uses Python's standard library and the local
macOS SDK's `sys/resource.h` layout. Output must be a new directory under `data/`.
For example, from the repository root:

```sh
go test -c -tags=integration -o data/residency-routing.test ./internal/routing
OPENMAPS_PREPARED="$PWD/data/prepared-scale/published" \
OPENMAPS_PERF_DB="$PWD/data/recursive-scale/northwest-candidate.sqlite" \
OPENMAPS_PERF_CASES="$PWD/internal/routing/testdata/northwest.json" \
OPENMAPS_RESIDENCY_OUT="$PWD/data/residency-query-snapshots" \
OPENMAPS_RESIDENCY_VERIFY=1 GOMEMLIMIT=3GiB GOGC=50 \
python3 scripts/routing-residency.py data/residency-query-run \
  data/residency-routing.test -test.run '^TestPreparedResidency$' -test.v
```

The diagnostic test captures heap, cumulative allocations, Go reservations,
faults, `ps` virtual/RSS values, `vmmap` aliases and `footprint` clean/dirty/swapped
categories around loading, verification, first/repeated routes and retirement.
Without `OPENMAPS_RESIDENCY_VERIFY=1`, it deliberately reproduces the former
second validation mapping. Its query outcomes are diagnostic; the retained
source suites separately assert route correctness. On Linux the test captures
`smaps` and `vmstat`; the Python process sampler currently requires macOS.

The same Python wrapper can run the real HTTP and temporary deployment lifetime
tests described in [routing scale](routing-scale.md). Set
`OPENMAPS_RESIDENCY_MAPS=1` for periodic `vmmap`/`footprint` snapshots. These probes
perturb latency and delay the nominal 100 ms sampler; timestamps are recorded.
Use separate runs without these probes for latency. Samples are observations,
not guaranteed lifetime maxima or enforceable budgets.

Clean mapped-file bytes reported by `footprint` help distinguish aliases from
physical file residency. They exclude unrelated file-cache pages and are not total
unique system memory. RSS can count the same file page more than once; charged
footprint excludes much clean file memory. Go's much larger virtual reservation
also differs from the explicit artifact size. Faults do not by themselves measure
storage reads. Host swap/compression counters include unrelated applications.
Neither `GOMEMLIMIT`, mmap nor `MADV_DONTNEED` establishes a process physical-memory
cap or a cold system cache. No host-wide cache purge or pressure generation is
performed by these harnesses.
