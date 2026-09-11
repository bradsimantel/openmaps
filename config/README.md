# Configuration

This directory contains the checked-in inputs needed to reproduce Open Maps data
artifacts. These are maintained source configurations, not generated lockfiles or
runtime server settings. Generated downloads, lookup generations, tiles and
receipts belong in the ignored `data/` directory.

## Places and geocoding

`places-geocoding.json` defines the shared Newport source snapshot used by both
Places and geocoding. It pins the Overture releases, source URLs, bounding boxes,
source checksums, attribution and normalized bundle checksum.
`cmd/places-geocoding-prepare` uses it to acquire and normalize source records;
`cmd/places-geocoding-import` uses it to verify the bundle before building
normalized Parquet and its DuckDB serving catalog. It is intentionally shared
because the two domains use the same businesses, addresses, areas and streets
while retaining distinct API behavior.

Normal builds must not edit this file to bypass a checksum mismatch. The explicit
maintainer workflow for an independently reviewed source change is
`places-geocoding-prepare -accept-reviewed-source-update`. This operation
requires `-fetch`, replaces reviewed source exports and accepts their source and
bundle checksums. A source transition that requires permanent identity mappings
supplies a separate file with `places-geocoding-prepare -identities`; no mappings
are needed by the current snapshot.

The maintained Newport Places smoke checks are Go data returned by
`importer.NewportPlacesQueryChecks`, not another config file. The
`places-geocoding-refresh compare -queries PATH` option and the `OPENMAPS_QUERIES`
integration-test variable still accept an alternate JSON file when evaluating a
different region or query set.

## Routing

`routing/scout-national-acquisition.json` is the validated acquisition plan for
the qualified national Scout generation. It pins the provider metadata and all
647 selected package checksums. Exact tile pins are supplied by the immutable
package receipts retained with the acquisition directory; preserve those receipts
with the downloaded packages. Post-build graph hashes and qualification outcomes
belong to the historical records under `docs/log/`, not this input plan. The
running server does not read the plan.

`routing/national-landmark-seeds.json` is a graph-construction input.
`routing/national-route-cases.json` is the maintained national qualification
suite. Historical snap-audit probes remain with the qualification record in
`docs/log/0034-national-scout-snap-probes.json`.

## Basemap

`basemap.json` pins the Protomaps source, extraction tool version, Newport bounds,
maximum zoom and expected PMTiles checksum. It is independent of the Places and
geocoding source data and is consumed only by `cmd/basemap`.
