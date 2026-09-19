# National Places/geocoding builds

Open Maps has a bounded-memory path for building normalized Places/geocoding
Parquet and its DuckDB serving catalog directly from pinned Overture Parquet.
It does not create a nationwide `Bundle`, GeoJSON export, or transport JSON
file. The Newport JSON workflow remains the default regional demonstration.

## Reproducible scope and pins

[`config/places-geocoding-us.json`](../config/places-geocoding-us.json) is the
production candidate definition. It pins Overture release `2026-08-19.0`, the
SHA-256 of that release's STAC catalog, each provider dataset prefix, and the
SHA-256 of the sorted exact Parquet-object URLs selected from the catalog. A
second digest pins each selected URL together with its content length and ETag.
Changing a release, catalog, object set, object version, scope, batch size, or
memory limit is a reviewed configuration change. Remote Parquet is range-read
with its ETag in `If-Match`; a changed object or a server that ignores byte
ranges aborts.

The initial national scope means the 50 states and District of Columbia. Four
closed WGS84 longitude/latitude envelopes cover the contiguous states, Alaska
on both sides of the antimeridian, and Hawaii. Addresses and divisions must
carry Overture country `US`.
Businesses with a country-bearing first address must also carry `US`; a
business without source country is retained when its point is inside an
envelope. Named road segments have no supported country field, so they are
included when their actual centerline intersects an envelope. This can retain
short Canadian or Mexican road portions near a rectangular border. That known
scope imprecision must be reviewed before public national activation; it does
not make memory or identity behavior unsafe.

Puerto Rico, the U.S. Virgin Islands, Guam, American Samoa, and the Northern
Mariana Islands are excluded from this initial definition. They require an
explicit later scope/configuration decision. Offshore points and line portions
inside an included envelope are retained; those outside are not. A line that
crosses an envelope is clipped at the closed boundary. Its display point is the
spherical-length midpoint of all retained portions, preserving source edge
order and linearly interpolating longitude/latitude within the selected edge.
It is a search marker, not a routing snap.

Included entity classes are Overture places as businesses, standalone
addresses, divisions as administrative/locality areas, and named
Transportation `subtype=road` segments as streets. Buildings are not currently
imported as lookup entities and are not conflated with addresses or businesses.
Unnamed roads, non-road segments, out-of-scope points, records without a
required display label, and labels/aliases/addresses that normalize to no
search token are explicit rejection rows with their original source record.
Routing remains the independent Scout snapshot; basemap tiles remain
independent Protomaps data.

Every division asset row is streamed into a temporary bounded DuckDB relation
so recursive parent closure can retain name-bearing parents outside the initial
envelope. Only selected areas, their selected parents, and relationships are
published. A required provider parent without a primary name cannot become a
valid area and is rejected rather than assigned a fabricated name.

## Bounded construction and output

The adapter submits at most 16,384 provider records, relationships, or
rejections per call. Four concurrent staging writers use independent DuckDB
connections and bulk appenders; temporary DuckDB tables own business/address
matching and
division ancestry. Business links require exact normalized number/street and
five-character postcode, one candidate within 50 spherical metres, and no
second qualifying candidate. Normalization externally sorts public identity
groups and retains only one entity's contributing source records in Go. The
national and qualification profiles use four source workers, four staging
writers, and four DuckDB threads. The concurrently open preparation and
normalization databases have
separate 4 GB and 8 GB limits, respectively, and the Go runtime has a 4 GiB
soft memory limit. The normalization database is checkpointed, closed, and
reopened after input staging so ingestion buffers do not remain resident during
normalization. Entity-key construction is unsorted. Entity normalization then
processes the sixteen leading hexadecimal public-ID buckets in lexical order,
sorting only one bucket at a time; this preserves deterministic global order
without a national-scale spill merge. Parquet dictionaries fall back to plain
encoding at 4 MiB per column. Serving-catalog construction uses four DuckDB
threads and a separately pinned 32 GB limit. Its national sorts, groups and
windows are split into disjoint ID, lexical, token and spatial partitions
containing no more than four million input rows. Posting generation additionally
uses 32,768-entity chunks, and an individual hot token is split by entity
sequence. A 48 GiB process supervisor is still required because these component
limits are not a hard whole-process RSS bound.

Public IDs continue to hash immutable source-qualified identity anchors.
Provider source IDs, release, original records, winning-attribute paths,
attribution, identity mappings, relationships, and rejection evidence are
retained. Normalized files use deterministic ordering, Zstandard, 32,768-row
groups, and rotate at a row-group boundary near 384 MiB. The output layout is
the same `entities*`, `source-records*`, `attribute-provenance*`,
`relationships*`, `rejections*`, `metadata.parquet`, `serving.duckdb`, and
`manifest.json` layout documented in [refresh and rollback](refresh.md).
Normalized Parquet checksums are reproducible. `data_sha256` binds the entities,
sources, provenance, relationships, and rejections while deliberately excluding
operational source-manifest metadata, so a tuning-only change can reproduce it.
`normalized_sha256` also binds that metadata. Physical DuckDB files are bound by
their own checksums but are compared logically rather than assumed to be
byte-reproducible.

## Commands

Check RAM and disk, download/verify only the small pinned catalog, and inspect
the exact remote asset set plus conservative candidate-row workspace estimate:

```sh
sysctl -n hw.memsize
df -h data
go run ./cmd/places-geocoding-prepare \
  -config config/places-geocoding-us.json -data data -fetch -preflight
```

The preflight reports geographic candidate rows separately from rows that must
actually be read. Division reads include the global parent set, so the workspace
coefficient named `estimated_peak_bytes_per_candidate_row` is conservatively
applied to the larger rows-read count, then increased by a measured 10% safety
margin. The preflight does not start a build. A
build is refused when its conservative estimate would leave less than the
checked-in 60 GiB free-disk floor on either the preparation-data volume or the
generation-output volume. When those are different filesystems, each is required
to pass the complete conservative estimate because temporary preparation and
generation files coexist.

Before a national build, run the checked-in qualification slice on the intended
host with the same concurrency and memory profile. Its
`expected_data_sha256` is the retained-data digest from the gate qualified on
the intended Linux/amd64 build host. A mismatch aborts before publication; the
complete normalized checksum is expected to change because its metadata records
the new controls:

```sh
go run ./cmd/places-geocoding-prepare \
  -config config/places-geocoding-us-gate.json -data data -fetch -preflight
go build -o /tmp/openmaps-places-prepare ./cmd/places-geocoding-prepare
go build -o /tmp/openmaps-run-bounded ./cmd/scout-run-bounded
/tmp/openmaps-run-bounded \
  -root "$PWD/data" -report "$PWD/data/us-parallel-gate-resources.json" \
  -rss-mib 49152 -reserve-gib 60 \
  /tmp/openmaps-places-prepare \
  -config config/places-geocoding-us-gate.json -data data \
  -stream-out data/openmaps-us-parallel-gate-20260819 \
  -checkpoint data/openmaps-us-parallel-gate-20260819-checkpoint \
  -audit data/openmaps-us-parallel-gate-20260819-audit.json
```

Do not run the national command until both that gate and the national preflight
pass on the provisioned host:

```sh
go build -o /tmp/openmaps-places-prepare ./cmd/places-geocoding-prepare
go build -o /tmp/openmaps-run-bounded ./cmd/scout-run-bounded
/tmp/openmaps-run-bounded \
  -root "$PWD/data" -report "$PWD/data/us-build-resources.json" \
  -rss-mib 49152 -reserve-gib 60 \
  /tmp/openmaps-places-prepare \
  -config config/places-geocoding-us.json -data data \
  -stream-out data/openmaps-us-20260819 \
  -checkpoint data/openmaps-us-20260819-checkpoint \
  -audit data/openmaps-us-20260819-audit.json
```

That command performs range acquisition, provider preparation, normalization,
catalog construction, checksum verification, and referential integrity
validation. It publishes only after every phase succeeds and refuses an
existing output. After input staging completes, it checkpoints and closes the
DuckDB staging database, records the preparation audit and exact build identity,
and atomically publishes the checkpoint directory. The identity covers the
output path, manifest and source pins, identity mappings, expected retained-data
checksum, memory and thread controls, clean Git revision, and OS/architecture.
Resume also validates all staging table counts, discards only derived work from
the failed attempt, and reruns the normal input integrity checks before rebuilding.

For a long-running national build, provide a separate normalized checkpoint.
It is published atomically only after normalized checksums and physical Parquet
row counts pass, and remains immutable after catalog success or failure:

```sh
/tmp/openmaps-run-bounded \
  -root "$PWD/data" -report "$PWD/data/us-build-resources.json" \
  -samples "$PWD/data/us-build-samples.ndjson" \
  -temporary-path "$PWD/data" -rss-mib 49152 -reserve-gib 60 \
  /tmp/openmaps-places-prepare \
  -config config/places-geocoding-us.json -data data \
  -stream-out data/openmaps-us-20260819 \
  -checkpoint data/openmaps-us-20260819-checkpoint -resume \
  -normalized-checkpoint data/openmaps-us-20260819-normalized \
  -audit data/openmaps-us-20260819-audit.json
```

After that marker exists, a catalog or validation retry does not repeat source
preparation or normalization:

```sh
/tmp/openmaps-run-bounded \
  -root "$PWD/data" -report "$PWD/data/us-catalog-resume-resources.json" \
  -samples "$PWD/data/us-catalog-resume-samples.ndjson" \
  -temporary-path "$PWD/data" -rss-mib 49152 -reserve-gib 60 \
  /tmp/openmaps-places-prepare \
  -config config/places-geocoding-us.json -data data \
  -stream-out data/openmaps-us-20260819 \
  -normalized-checkpoint data/openmaps-us-20260819-normalized \
  -catalog-resume \
  -catalog-threads 4 -catalog-memory-limit 32GB \
  -audit data/openmaps-us-20260819-audit.json
```

The catalog-only command requires the checkpoint's exact build identity. It
refuses an existing output and publishes through a new `.building` directory,
so interruption cannot turn a partial database into a candidate.
The [historical national catalog qualification](log/0058-national-catalog-memory-investigation.md)
records the first actual-cardinality result for this boundary and partitioned
catalog; it is evidence for that revision, not a substitute for a new run's
preflight and supervisor controls.

If normalization, catalog construction, or validation fails after that marker
is published, preserve the checkpoint and rerun the same supervised command
with `-resume`. Do not use `-resume` after changing the revision, configuration,
controls, identities, expected checksum, output, or target architecture:

```sh
/tmp/openmaps-run-bounded \
  -root "$PWD/data" -report "$PWD/data/us-build-resources.json" \
  -rss-mib 49152 -reserve-gib 60 \
  /tmp/openmaps-places-prepare \
  -config config/places-geocoding-us.json -data data \
  -stream-out data/openmaps-us-20260819 \
  -checkpoint data/openmaps-us-20260819-checkpoint -resume \
  -audit data/openmaps-us-20260819-audit.json
```

An interruption before the marker completes still restarts source ingestion;
a leftover `-checkpoint` path ending in `.building` is deliberately refused and
must be inspected as an incomplete failed-run directory. A resumed attempt
rebuilds normalization and catalog artifacts from the durable staged inputs and
still enforces the expected data checksum and complete verification. On success,
the candidate is atomically published and the checkpoint is removed. Remote
Parquet is not retained locally; retaining an independently archived copy for
long-term rebuilds remains an operator decision that this command does not
implement.

The 48 GiB sampled RSS ceiling deliberately leaves roughly 14 GiB on the 64 GB
host for the kernel, filesystem cache, and sampling delay. The checked-in gate
uses the same four-worker, four-thread, and memory controls as the national
profile and must reproduce its expected retained-data checksum before these
controls are used for a later nationwide rebuild. Progress is logged every
30 seconds and whenever a remote asset completes.

Before activation, run representative queries against the candidate, compare
it with the selected generation using reviewed scope-appropriate query
expectations, and follow the review/activation commands in
[refresh and rollback](refresh.md). The maintained comparison command defaults
to Newport expectations, so a national operator must pass an explicit
`-queries` file:

```sh
go run ./cmd/places-geocoding-refresh compare \
  -candidate data/openmaps-us-20260819 -queries config/us-query-checks.json \
  -report data/us-refresh-report.json
go run ./cmd/places-geocoding-refresh review \
  -report data/us-refresh-report.json -review data/us-refresh-review.json \
  -reviewer NAME -reason 'Reviewed national scope and query changes'
go run ./cmd/places-geocoding-refresh activate \
  -candidate data/openmaps-us-20260819 -report data/us-refresh-report.json \
  -review data/us-refresh-review.json
```

The checked-in `config/us-query-checks.json` is the national autocomplete
expectation set. It covers raw city and street names, city/state disambiguation,
ZIP codes, landmarks, contextual businesses and negative queries. A candidate
does not redefine those expectations: changes to IDs, kinds, names or stated
intent require separate review. The [initial national relevance baseline](log/0059-national-places-relevance-baseline.md)
is historical evidence: the first national research artifact passed only 18 of
99 expectations and must not be activated without ranking corrections.
Autocomplete now interprets supported comma-separated `city, state` and
`street, city, state` inputs using exact primary names, the retained division
hierarchy and locality-relative street distance. This runtime behavior is
compatible with the existing normalized generation; it does not approve that
generation or replace a complete rerun of this expectation set on the exact
national artifact.
For unstructured exact geographic names, the runtime also adapts retained
source evidence into provider-independent prominence and settlement tiers.
City-class localities are ordered by supplied prominence; exact regions precede
towns, villages, hamlets and smaller administrative areas; stable entity order
breaks remaining ties. The schema-2 source locators make this compatible with
the existing artifact even though prominence and settlement class were not
projected into its normalized entity or serving-search rows. A future artifact
could project that evidence directly for lower read amplification, but that is
a proposal rather than a required rebuild or activation decision.
Unstructured exact destination names similarly adapt the retained Overture
place taxonomy and existence confidence into provider-independent category,
specificity and coarse reliability evidence. This can prefer a monument,
museum, stadium, attraction or other mapped destination over an inappropriate
same-name business, street or minor area while preserving exact city and region
ordering. Confidence is not treated as popularity, and unrelated taxonomy
families are not assigned a fabricated global order. Source-row inspection is
limited to 64 exact place candidates per request; more ambiguous names retain
the compact catalog order and require a future projected signal if stronger
ranking is needed. This behavior works with the existing schema-2 artifact and
does not merge or reclassify entity kinds.
Authentication, billing, full Google field coverage, exact non-rectangular
national road clipping, buildings, territories, and a production hosting
topology remain unsupported or undecided.

Free-form address robustness is measured separately with the pinned external
MESSY STREETS gold tier. It is an opt-in diagnostic, not part of routine tests
and not an autocomplete oracle:

```sh
go run ./cmd/places-geocoding-benchmark \
  -lookup data/openmaps-us-20260819 \
  -dataset-config config/benchmarks/messy-streets-gold.json \
  -dataset data/benchmarks/messy-streets-gold.jsonl.gz -fetch \
  -report data/benchmarks/messy-streets-openmaps.json
```

The report binds the exact lookup manifest and dataset checksum. It reports
verbatim and component-canonical queries independently and measures returned
address coordinates against the corpus coordinates at 100 m, 1 km and 10 km.
