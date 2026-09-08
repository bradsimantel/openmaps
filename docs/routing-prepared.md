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

`routing-prepared-le64-v1` uses a 4 KiB header and ordered aligned sections.
Integers have explicit little-endian widths; coordinates and costs retain their
float64 bits. It uses the checked 64-bit little-endian Linux/macOS mapping ABI.
Unknown versions and unsupported ABIs fail. No native Go pointer representation
is stored. Section dimensions are checked against actual file size before views
are created, with overflow-safe extent checks and canonical zero padding.

| Runtime structure | Prepared representation / loading |
| --- | --- |
| Coordinates, directed edges, CSR offsets and adjacency | Mapped numeric arrays, including source node identities and directional costs |
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

Artifacts and SQLite files must remain immutable while in use. Read leases cover
search and reconstruction; HTTP deployment leases extend through encoding. Close
waits for readers, unmaps once, and rejects later queries. Failed unpublished
loads release mappings. Failed replacement preserves the prior handler and marks
health degraded. Prepared rollback validates the receipt before changing selection.

## Measurement and remaining gates

The [historical prepared-loading evaluation](log/0022-directly-loadable-routing-snapshots.md)
records phase measurements, predeclared budgets, all three regional workloads and
lifecycle verification. These are desktop file-cache observations. Integrity
validation reads every page, so subsequent first requests are not cold-disk tests.
`MADV_DONTNEED` is only a residency hint; it does not establish a cold system cache.
A controlled cold-cache host experiment remains unperformed.

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
