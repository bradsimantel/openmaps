# National Places/geocoding streaming gate

**Date:** 2026-09-11

**Starting revision:** `117599667c0e8309a9e0eb434df84864ea4dc27f`

**Host:** Apple arm64, 16 GiB RAM, macOS

**Outcome:** bounded streaming implementation qualified at a measured 6.105% candidate-row gate; no national build started

## Scope and selection

The checked-in gate is Overture release `2026-08-19.0` over the closed WGS84
envelope `[-125, 41.9, -104, 49.1]`, with address/place region filtering for
Washington, Oregon, Idaho, Montana, and Wyoming. This combines Seattle and
other dense business/address coverage with sparse Montana/Wyoming coverage,
multiple state and locality boundaries, named roads, boundary-crossing road
geometry, and the complete global division set needed for recursive parents.

The slice was selected from Parquet footer statistics, not intuition. Its
14,212,721 geographic candidate rows are 6.10467% of the national config's
232,817,324. The exact catalog, selected object URL sets, and URL/content-length/
ETag version sets are SHA-256 pinned in
`config/places-geocoding-us-gate.json`. The national comparison below uses
`config/places-geocoding-us.json`; both pin the same Overture release and
catalog SHA-256 `35ebf80fa9ae679ce353d7b8a58a95c1522471aec093967614fb1bc96254c71d`.

| Source | Selected objects | Object bytes | Row groups read | Rows read | Geographic candidate rows |
| --- | ---: | ---: | ---: | ---: | ---: |
| Place | 1 | 701,485,400 | 63 | 1,155,150 | 1,155,150 |
| Address | 3 | 2,114,001,014 | 144 | 8,288,721 | 8,288,721 |
| Division | 1 | 578,060,705 | 256 | 4,658,700 | 117,278 |
| Segment | 3 | 2,163,287,072 | 219 | 4,651,572 | 4,651,572 |
| **Total** | **8** | **5,556,834,191** | **682** | **18,754,143** | **14,212,721** |

All division row groups are read so parents outside the gate can be retained.
The final preparation decoded 15,697,477 exact in-scope/global-parent source
rows after row-level geometry filtering and transferred 2,674,298,767 bytes by
HTTP range requests. Only the 238,537-byte pinned catalog is retained as input;
remote provider Parquet is not downloaded to local files.

The successful command was:

```sh
/tmp/openmaps-run-bounded \
  -root "$PWD/data" -report "$PWD/data/us-gate-resources-20260911-retry10.json" \
  -rss-mib 6144 -reserve-gib 60 \
  /tmp/openmaps-places-prepare \
  -config config/places-geocoding-us-gate.json -data data \
  -stream-out data/us-gate-20260819 \
  -audit data/us-gate-audit-20260911.json
```

## Output and integrity

The completed immutable generation contains 8,079,245 entities:

| Kind | Entities |
| --- | ---: |
| Address | 6,067,093 |
| Business | 759,301 |
| Street | 1,245,001 |
| Area | 7,850 |

Relationships comprise 96,167 unique business-to-address links and 7,849
area-parent links. Another 12,398 business address candidate sets were
ambiguous and deliberately produced no relationship.

| Rejection reason | Rows |
| --- | ---: |
| `missing_primary_name` | 2,439,197 |
| `outside_configured_scope` | 504,331 |
| `non_road_segment` | 23,841 |
| `outside_manifest_hierarchy` | 276 |
| `missing_display_name` | 7 |
| `missing_search_text` | 3 |
| `outside_manifest_geometry` | 3 |
| **Total** | **2,967,658** |

The normalized Parquet files occupy 3,919,028,917 bytes. Shards by role are:
two entity, six source-record, one provenance, one relationship, two rejection,
and one metadata file (13 Parquet files total). Provenance contains 24,433,131
rows. `serving.duckdb` is 1,701,064,704 bytes. The complete generation,
including its 2,161-byte manifest, is 5,620,095,782 logical file bytes.

The normalized logical checksum is
`901e9349ea9e976bf63022744cd4eb565f4cb4fe06c16b167882fb69eea090bd`.
Every constituent file has a manifest-bound SHA-256. The builder and a separate
server reopen both completed full verification: manifest/file hashes and row
counts, entity/search/locator coverage, posting and prefix coverage, exact and
spatial address coverage, source/provenance locators, no duplicate source keys,
no dangling or duplicate relationships, valid unique rejections, and no
persistent catalog indexes. Because this build has no explicit identity map,
the strengthened verifier also scanned all 8,079,245 source rows and found zero
public IDs differing from
`om_ + first-32-hex(sha256("openmaps:entity:v1:" + source_key))`.

The maintained small-fixture test feeds the same bundle through the existing
regional path and the bounded stream in reversed batches of at most two, then
requires the same normalized checksum. Parent/link tests exercise an outside
administrative parent and spillable unique/ambiguous address matching. The
gate's recorded `max_submitted_batch` is exactly 2,048 while total source rows
are 15.7 million.

## Time, memory, and disk

| Phase | Wall time |
| --- | ---: |
| Place source | 119.390 s |
| Address source | 732.459 s |
| Division source | 327.748 s |
| Segment source | 388.061 s |
| Joins and hierarchy | 9.191 s |
| Complete input staging | 1,576.981 s |
| Normalization and Parquet | 275.957 s |
| Serving catalog | 222.872 s |
| Final validation | 17.865 s |
| Builder total | 2,111.438 s |
| Supervised command | 2,114.937 s |

Preparation and normalization each use a 1 GB DuckDB limit. Catalog creation
uses one DuckDB thread and a separately pinned 3 GB limit. The Go runtime soft
limit is 1.5 GiB; Parquet dictionary state is limited to 4 MiB per column; the
supervisor enforced a 6 GiB sampled whole-process RSS ceiling. Successful-run
sampled peak RSS was 4,518,576,128 bytes (4.21 GiB); platform child maximum RSS
was 4,524,294,144 native units.

The run began with 177,963,401,216 free bytes and never fell below
148,678,201,344, a sampled high-water delta of 29,285,199,872 bytes
(27.27 GiB). It finished with 171,127,726,080 free bytes. The mandatory 60 GiB
(64,424,509,440-byte) floor was never approached. The original raw coefficient
estimated 28,806,363,648 bytes, 1.7% below the observed delta; current preflight
adds a measured 10% operational margin and reports 31,687,000,012 bytes for this
same gate.

Every failed build used a hidden generation directory and left no published
candidate. The builder removed its preparation/normalization/spill directories
on failure; the successful preparation spill was also removed before publish.
The completed generation, audit JSON, final resource report, and small failure
resource reports remain locally under ignored `data/` paths as evidence. No
provider Parquet, generated catalog, audit, report, or machine path is committed.

## Query verification

The verified direct server returned:

- `Starbucks Seattle` autocomplete: a Seattle Starbucks business ID whose
  details resolved with name, address, coordinates, types, and attribution;
- `Seattle`: an administrative/locality area and a distinct street entity;
- `Main Street`: distinct area, street, and business entities rather than type
  conflation;
- `Boise` and `Yellowstone`: sparse/interior area and business results;
- details for business `om_a45407c10b72cd9b0c70ac0ca7361d2d`, area
  `om_70b4923456a41e3b8fb88b35061bc7cc`, and street
  `om_000549e5431bd6e7519a92aae9ab4ee4`;
- forward geocoding `2210 West Main Street`: one matched address; reverse
  geocoding its coordinate: a documented two-candidate ambiguous response with
  the nearest supported address first.

Latency includes a fresh localhost HTTP connection per request after startup
verification. Autocomplete mixes the four dense/sparse/type-varied queries
`Starbucks Seattle`, `Main Street`, `Boise`, and `Yellowstone`; details cycles
the business, area, and street IDs above.

| Operation | Samples | p50 | p95 | p99 | max |
| --- | ---: | ---: | ---: | ---: | ---: |
| Autocomplete | 120 | 43.302 ms | 173.422 ms | 178.514 ms | 205.817 ms |
| Place details | 120 | 6.265 ms | 6.982 ms | 9.211 ms | 11.409 ms |
| Forward geocoding | 100 | 15.692 ms | 16.553 ms | 16.696 ms | 22.019 ms |
| Reverse geocoding | 100 | 29.443 ms | 30.333 ms | 35.498 ms | 35.546 ms |

These are scale-gate, single-process localhost observations, not a national SLO.

## Failures and corrections

The gate was intentionally carried through all safety and integrity failures:

1. Global divisions were initially normalized before selection, exposing a
   name-less outside record. Division rows are now spill-staged and only selected
   rows/required parents are normalized; candidate exclusions remain explicit.
2. Selected address/place records without a usable display name now become
   `missing_display_name` rejections instead of failing late.
3. The regional 128 MB and an attempted 256 MB normalization limit exhausted at
   122 MiB and 243 MiB. Schema 2 now pins 1 GB preparation/normalization limits.
4. A malformed optional provider website passed a prefix-only check and failed
   output validation. Optional websites now require an HTTP(S) URL with a host;
   the complete raw provider record remains in provenance.
5. An attempt exceeded the then-4 GiB supervisor ceiling after Parquet emission.
   The Go 1.5 GiB soft limit, 4 MiB Parquet dictionary limit, completed-asset
   range-cache release, and final 6 GiB supervisor cap bound the successful run.
6. The regionally qualified 256 MB serving-catalog limit exhausted. One DuckDB
   thread reduced duplicate state, but 1 GB still exhausted at 953.6 MiB. A
   separately pinned 3 GB catalog limit completed with 4.21 GiB peak process RSS.
7. Final validation then found three symbol-only accepted records with no search
   posting. They are now explicit `missing_search_text` rejections, including
   alias-aware division-parent eligibility. Retry 10 completed with full search
   coverage.

The resource reports for the two late catalog failures measured 3.98 and 4.85 GB
sampled peak RSS; the earlier process-cap abort measured 4.31 GB. These failures
never crossed the 60 GiB disk reserve and left no published output.

## National estimate and readiness

Pre-merge review found that the original negative-longitude Alaska envelope
omitted the state's eastern-hemisphere Aleutian extent. The final national
configuration adds the disjoint `[172, 51, 180, 54]` envelope and refreshes its
exact asset/version pins. This selects one additional place object and one
additional segment object; the gate build itself is unchanged.

The final national preflight reads exact metadata for 7 place, 12 address, one
division, and 29 segment objects. It reports 232,817,324 geographic candidate
rows, 237,057,825 rows actually read, and a calibrated peak workspace estimate
of 400,532,901,120 bytes. Current free space was 173,802,328,064 bytes, so this
workstation cannot pass `estimate + 60 GiB <= free`; no national build began.

Scaling generation bytes by the measured candidate-row factor
`232,817,324 / 14,212,721 = 16.3809` gives:

| Retained component | National point estimate |
| --- | ---: |
| Normalized Parquet | 64.20 GB |
| Serving catalog | 27.86 GB |
| **One lookup generation** | **92.06 GB** |
| Lookup + 40 GB routing + 20 GB basemap | **152.06 GB** |
| Two lookup generations + routing + basemap | **244.12 GB** |

Scaling observed workspace by rows actually read gives 370.17 GB; the calibrated
preflight's 400.53 GB is the operational value. A first build alongside the
40 GB routing and 20 GB basemap assumptions plus the 60 GiB reserve needs at
least 524.96 GB free. Rebuilding while retaining a prior 92.06 GB lookup needs
at least 617.02 GB free. The recommendation is a 1 TB build volume with at least
650 GB free at start and 32 GiB RAM; retain the 6 GiB process guard initially
and raise it only from measured evidence.

The implementation is ready for a controlled nationwide attempt on that
provisioned host: acquisition is exact-version pinned, preparation is bounded,
publication is atomic, and preflight refuses the present workstation. It is not
ready to run nationwide on this workstation. Public national activation still
requires a reviewed national query expectation set and resolution of the
documented rectangular-envelope road spill into Canada/Mexico/nearby offshore
areas. Authentication, production hosting topology, territories, buildings,
and interrupted remote-range resume remain explicitly unsupported or deferred;
they are not hidden assumptions of this gate.

## Verification commands

The final tree passed:

```sh
gofmt -w <changed Go files>
go test ./...
go vet ./...
go run ./cmd/places-geocoding-prepare
git diff --check
```

The regional prepare command reproduced reviewed bundle checksum
`588ec72c92b6e167fd9cad53559569f7e3b298a9fdd6ce57dbd4c7ba806e9af4`.
The final gate build performed its own complete `Verify`, and server startup
performed the complete verifier again before the latency run.
