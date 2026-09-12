# Places/geocoding refresh and rollback

The refresh path publishes immutable normalized Parquet plus a derived DuckDB
serving catalog. It never rewrites an active generation or selects a legacy
fallback. Routing and basemap artifacts have separate
selection and lifetime.

## Normal Newport preparation

From the repository root:

```sh
go run ./cmd/places-geocoding-prepare -fetch
go run ./cmd/places-geocoding-import
```

Preparation verifies every pinned provider object and creates the deterministic
normalized provider-record stream. Import consumes its record and relationship
arrays incrementally, uses an externally spillable DuckDB normalization stage,
and retains at most one identity group's source records in Go memory. It emits
ordered Zstandard Parquet with 32,768-row groups. Large relations rotate near
384 MiB at row-group boundaries; a small regional relation remains one shard.

The generation contains:

| Artifact role | Contents |
| --- | --- |
| `entities*.parquet` | Provider-independent entities, search fields, coordinates, attribution and evidence locators |
| `source-records*.parquet` | Complete original source records, source IDs, releases, priorities, attributes and paths |
| `attribute-provenance*.parquet` | Winning attribute, source key and source path |
| `relationships*.parquet` | Validated business/address and area-parent relationships |
| `rejections*.parquet` | Stable source key, explicit rejection reason and original record |
| `metadata.parquet` | Pinned source manifest and identity mappings |
| `serving.duckdb` | Query projections, sorted tokens/postings, short-prefix heads, exact addresses, spatial grid and Parquet locators |
| `manifest.json` | Schema, coordinate order, logical normalized checksum, and per-file checksums/row counts |

Normalized Parquet bytes and the logical generation checksum are deterministic.
DuckDB physical bytes are not assumed reproducible; each derived catalog has its
own manifest-bound checksum. The regional JSON path caps normalization staging
at 128 MB; schema 2 streaming manifests explicitly configure preparation and
normalization (currently 1 GB for the national candidate). Schema 2 catalog
construction uses one DuckDB thread and its separately configured 3 GB limit;
regional JSON builds retain their 256 MB catalog limit. All may spill inside
the temporary generation directory. Interrupted builds leave no published
generation, and the builder refuses an existing output directory.

The normal import initializes `data/lookup-selection.json`, or advances it while
retaining the previous generation. For a controlled build without selection:

```sh
go run ./cmd/places-geocoding-import \
  -bundle data/newport.json \
  -out data/openmaps-2026-09-11 \
  -selection ''
```

`-accept-reviewed-source-update` is only for deliberately changing pinned source
objects. `-accept-reviewed-normalization-update` records a reviewed normalization
checksum change without changing source pins. Neither flag is a way to bypass an
unexplained mismatch.

## Compare, review and activate

Build a candidate into a new directory:

```sh
go run ./cmd/places-geocoding-refresh build \
  -candidate data/openmaps-candidate
```

Compare it with the currently selected generation (or pass `-baseline`):

```sh
go run ./cmd/places-geocoding-refresh compare \
  -candidate data/openmaps-candidate \
  -report data/refresh-report.json
```

The comparison verifies both complete manifests, then uses DuckDB joins over
Parquet rather than Go maps. It reports entity and relationship changes, rejects
kind changes and public-ID changes for continuing source records, checks the
maintained autocomplete expectations, and verifies that every returned ID has
matching details.

After inspecting source evidence and uncertain additions/removals, bind a review
to the exact report:

```sh
go run ./cmd/places-geocoding-refresh review \
  -report data/refresh-report.json \
  -review data/refresh-review.json \
  -reviewer NAME \
  -reason 'Summary of reviewed changes and uncertainty'

go run ./cmd/places-geocoding-refresh activate \
  -candidate data/openmaps-candidate \
  -report data/refresh-report.json \
  -review data/refresh-review.json
```

Activation refuses a stale report, changed candidate, changed current selection,
failed comparison, or incomplete review. It writes and syncs one selection JSON
atomically. A lock serializes local writers; a crash can leave the lock, which
must be removed only after confirming no writer remains.

The server opens and verifies the candidate before the swap. Existing requests
retain their old-generation lease through response encoding, including a routing
request resolving both endpoints. Failed reloads retain the previous store and
appear in `/healthz`.

## Rollback and retention

```sh
go run ./cmd/places-geocoding-refresh rollback
go run ./cmd/places-geocoding-refresh status
```

Rollback verifies and selects `previous`, then makes the displaced current
generation the new rollback target. Keep current and previous generation
directories immutable and available. Do not treat absence from a regional
snapshot as proof of real-world deletion or closure.

The initial refresh records remain historical evidence in
[logs 0004–0007](log/0004-newport-refresh-investigation.md). They are not the
current artifact or rollback procedure.
