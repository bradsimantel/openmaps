# Configuration

This directory contains the checked-in inputs needed to reproduce Open Maps data
artifacts. These are maintained source configurations, not generated lockfiles or
runtime server settings. Generated downloads, databases, tiles and receipts belong
in the ignored `data/` directory.

## Places and geocoding

`places-geocoding.json` defines the shared Newport source snapshot used by both
Places and geocoding. It pins the Overture releases, source URLs, bounding boxes,
source checksums, attribution and normalized bundle checksum. `cmd/prepare` uses
it to acquire and normalize source records; `cmd/import` uses it to verify the
bundle before building SQLite. It is intentionally shared because the two domains
use the same businesses, addresses, areas and streets while retaining distinct API
behavior.

Normal builds must not edit this file to bypass a checksum mismatch. The explicit
maintainer workflow for an independently reviewed source change is
`prepare -update-config`. A source transition that requires permanent identity
mappings supplies a separate file with `prepare -identities`; no mappings are
needed by the current snapshot.

The maintained Newport Places smoke checks are Go data returned by
`importer.NewportPlacesQueryChecks`, not another config file. The
`refresh compare -queries PATH` option and the `OPENMAPS_QUERIES`
integration-test variable still accept an alternate JSON file when evaluating a
different region or query set.

## Routing

`routing.json` pins the qualified national Scout generation and its complete set
of selected package and tile checksums. It is used to reproduce and verify the
national routing snapshot; the running server does not read it.

## Basemap

`basemap.json` pins the Protomaps source, extraction tool version, Newport bounds,
maximum zoom and expected PMTiles checksum. It is independent of the Places and
geocoding source data and is consumed only by `cmd/basemap`.
