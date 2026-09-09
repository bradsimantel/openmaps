# Initial national graph audit and regional landmark acceleration

Date: 2026-09-09. Baseline: `d24c604`, uncommitted implementation following
historical [0030](0030-persistent-scout-regional-candidate.md). This records an
intermediate milestone. It does not certify a working national candidate.

Later work in [0032](0032-national-closure-and-reverse-edge-identity.md) expands
the graph and replaces the v1 reverse landmark enumeration after a national
many-to-one opposing-index failure. The v1 landmark artifacts and commands below
are historical; current serving requires validated v2 vectors.

## Acquisition and persistent graph

The initial US/Canada/Mexico selection plus all 34 catalog omissions completed:
**620 packages, 8,921,997,783 compressed bytes**. Full provider catalog/digest
checks before and after acquisition found no generation change. Every retained
package has MD5, SHA-256, length, URL and generation metadata in its receipt.
The source generation remains `2026-06-20_07:12`, tile version 3.4.0, dataset
183131145. Exact OSM cutoff and production build inputs remain unverified.

`national-prepared/` contains **32,933 tiles, 23,848,943,616 padded bytes**.
Tile-spool SHA-256:
`0561940bd93277e797d25bb75d2fe78e4946b43c31d47f98fa29409a843aa134`.
Receipt SHA-256:
`2ce3a19cd8413de2adbd881deb40bb999e99443212c2832156055e29865adb8c`.
Paths in this record are relative to `data/scout-national-20260909/` unless stated.

| Phase | Elapsed seconds | Sampled peak RSS bytes | Child resource peak RSS bytes |
| --- | ---: | ---: | ---: |
| Streaming graph preparation | 614.574 | 45,023,232 | 66,158,592 |
| Forward turn index | 19.669 | 193,150,976 | 199,000,064 |
| Reverse turn index | 20.412 | 193,560,576 | 194,183,168 |
| Geographic lower bound | 120.809 | 195,952,640 | 195,952,640 |
| Complete graph audit with bounded diagnostics | 230.703 | 368,541,696 | 408,240,128 |

No disk/RSS supervisor budget triggered. Resource counters are observations on a
shared Apple M5 / 16 GiB macOS host, not hard memory limits or isolated benchmarks.
The 32 GiB free-disk reserve remains in force. Some independent small verification
or acquisition work overlapped these construction phases; physical cold disk was
not measured. Raw commands and observations are in the matching `.resources.json`.

The graph has **119,406,014 nodes, 277,713,653 directed edges, 3,952,923 shortcuts
and 27,576,455 hierarchy transitions**. Search ignores shortcuts. Forward turns
have 77,551 rules (14,781 timed), 206,498 states and 206,497 transitions. The
6,619,136-byte turn file has SHA-256
`8583f1f1869e48b57bf0de19339db5c25cbf6c5a64a5d60ec4ab7ff6b6574790`.
The access-table scan verified that no source access rule belongs to an edge
with a zero access-restriction mask. The runtime fast path is enabled only by
that validated receipt; older receipts retain full lookup behavior.

## Source findings and dependency expansion

The first audit stopped at a geometry mismatch. The extended audit retained
bounded examples and completed every tile while still returning failure:

* Edge `2/421920/94`, OSM way 620313011, in package 499: the decoded node is
  `[-176.5158615,-16.75]`, while the shape begins `[179.999979,-16.805363]`.
  Its endpoint tile `2/421919/0` is missing. This is a source record near Fiji,
  carried in a package selected by the provider's Hawaii catalog entry.
* Two opposing records on OSM way 655299344 near `[134.5,7.3593]` have a 1.545 m
  source-node/shape disagreement. All three findings lie outside the target.

No source geometry was silently repaired or omitted to turn this into a global
audit pass. `national-audit-2.json` retains all three findings, distributions,
tile records and **701 distinct missing referenced tiles**. The raw global
selection is not a fully closed or globally verified graph.

An independent geographic check uses the Census
[2025 state cartographic boundaries](https://www.census.gov/geographies/mapping-files/2025/geo/carto-boundary-file.html),
downloaded from the official `cb_2025_us_state_500k.zip` URL. The file is
3,245,373 bytes, SHA-256
`9cbfe171dad1555e11770c981d8f4db9e687a65c86f5bdae684eeb487e2e9b80`.
`scripts/scout-coverage.py` found **no missing referenced tile rectangle touching
the 50-state/DC polygons, and no recorded geometry finding inside them**. These
are simplified 1:500,000 polygons, not surveyed boundaries or evidence that OSM
contains every road. Canada likewise had no missing tile references in the
coordinate inventory; this latter observation was not a country-polygon audit.

Missing mainland dependencies are south of Mexico. The catalog's Central America
selection adds **23 packages, 250,915,412 bytes**, producing **643 packages and
9,172,913,195 compressed bytes**. Acquisition of that same pinned generation
completed; `national-closure-prepared/` is being built as a new immutable output.
The first graph and all failed audit evidence are preserved. Mainland component
closure still needs to be established after the expansion.

## Directed landmark implementation and regional evidence

The maintained [Scout contract](../routing-scout.md) describes the relaxed graph,
conservative float32 bounds, bounded construction, resumable vectors and serving
caches. Ordinary search retains all constraints; no upstream hierarchy pruning
or shortcuts are introduced. Synthetic tests cover decrease-key heap order,
500 directed partial-endpoint combinations on the restricted fixture, infinite
component bounds, quantization and rejection of corrupted persisted vectors.

The regional graph was reused by hardlinking immutable graph/receipt/potential
files into `regional-indexed/`; turn indexes were rebuilt with the validated
zero-mask lookup fast path. This is not an independently decompressed rebuild.
Two landmarks at Newport and Boston produced four 23 MiB vectors in **9.949 s**,
with observed peak RSS **501,415,936 bytes**. Regional vectors contain missing
external references and therefore do not certify a closed regional graph.

Newport–Boston still returns 114,050.914969 m and 4,646.570343 estimated seconds.
With 128 MiB graph cache, three route times were **32.386 / 21.764 / 20.780 ms**,
starting with empty application caches. Total allocations were **90,786,264
bytes across all three**; graph pages retained 66,125,824 bytes with no evictions.
The final search settled 365 states and created 603 labels. The destination is
itself a landmark, so these timings are not representative national evidence.

The held-out Providence–Worcester pair
`[-71.4128,41.8240]` → `[-71.8023,42.2626]` returns **63,179.289377 m and
2,476.996958 estimated seconds**. Its landmark search settled 59,771 states,
created 66,651 labels and took **198.176 ms**, versus 507.315 ms bidirectional
search in that verification run. Both, plus ordinary Dijkstra, agree. Newport,
Boston–Cambridge and Newport–Boston also agree and pass source-path checks.
Raw output: `regional-alt-verification.log`, `regional-alt.json` and resource
receipts. A forced-chain-only trial had reduced labels without improving elapsed
time; that unsuccessful intermediate optimization was not called a performance win.

## Reproducible checks and current limits

```sh
OPENMAPS_SCOUT_PREPARED="$PWD/data/scout-national-20260909/regional-indexed" \
OPENMAPS_SCOUT_LANDMARKS="$PWD/data/scout-national-20260909/regional-landmarks" \
  go test -tags integration ./internal/routing/valhallatiles \
  -run '^TestPreparedScoutRegional$' -count=1 -v
python3 scripts/scout-coverage.py \
  --boundaries data/scout-national-20260909/cb_2025_us_state_500k.zip \
  --audit data/scout-national-20260909/national-audit-2.json
```

National vectors, all-state routes, Alaska/Canadian/Mexican connectivity, island
separation, sustained national HTTP and national snapshot replacement remain
unqualified at this milestone. Source decoding/clipping are shared primitives;
the new offline path verifier independently checks paths and source prohibited
sequences, not the upstream provider's correctness or original OSM fidelity.
No commit, push, purchase or active deployment change was made.
