# Geocoding address components

2026-09-07 · Working tree based on `ee8a74c`; retained August baseline and July candidate.

Historical decision and verification. Current behavior is maintained in
[geocoding.md](../geocoding.md). This supersedes the empty-component decision in
[0008](0008-geocoding-contract.md).

## Contract and implementation

The empty `address_components` array was an implementation omission. Both retained
snapshots preserve structured Overture number, street, postcode, country and US
state values. Returning these does not require another source or rebuilding the
reviewed databases.

Official documentation checked on this date:

- [Google Geocoding v3 response](https://developers.google.com/maps/documentation/geocoding/guides-v3/requests-geocoding): components use `long_name`, `short_name` and `types`.
- [Overture address schema](https://docs.overturemaps.org/schema/reference/addresses/address/): address levels are country-dependent; the US layout is state followed by locality.

At geocoder load, join the address attribute's winning provenance to its retained
source record. The concrete `internal/importer/addressdata` adapter projects
Overture fields into `places.AddressComponents`; the geocoding domain carries
these with each result, and `internal/api` translates them to Google v3 objects.
This is a read-time projection from already imported data, not a persisted schema
change. Parsing remains in import code. Matching and ranking still use the existing
entity labels and coordinates.

Require source labels to agree with the entity's name and formatted address.
Do not borrow parts from a losing source. Unknown providers or conflicting labels
yield no components; malformed selected Overture JSON fails loading. Retained raw
records, source IDs, release versions and attribute provenance remain unchanged.

For `364 Bellevue Avenue`, all eight results now include:

```json
[
  {"long_name":"364","short_name":"364","types":["street_number"]},
  {"long_name":"BELLEVUE Avenue","short_name":"BELLEVUE Avenue","types":["route"]},
  {"long_name":"Rhode Island","short_name":"RI","types":["administrative_area_level_1","political"]},
  {"long_name":"United States","short_name":"US","types":["country","political"]},
  {"long_name":"02840","short_name":"02840","types":["postal_code"]}
]
```

The two English code expansions are explicit API formatting. Other source values
retain their spelling in both name fields. Missing locality, county and unit are
not inferred, including from request context, postcode or adjacent features. No
E911 enrichment was imported. Eight IDs and coordinates remain distinct, with
`APPROXIMATE` geometry. The `components` request filter remains unsupported.

## Verification

- `gofmt`, `go test ./...` and `go vet ./...` passed.
- Synthetic tests cover forward/reverse component shapes, normalized input,
  Newport context without invented locality, missing fields, unsupported provider
  and country layouts, source conflicts, malformed JSON and provenance priority.
- The 26-case retained-data benchmark passed on both snapshots, now checking
  component values against retained Overture records as well as existing identity,
  coordinate, outcome and distance expectations. All 8,545 address entities remain
  identical across the snapshots; no address identity or coordinate changes.
  Output: ignored `data/geocoding-components-benchmark.txt`.
- Restarted the local deployment with the fix. Live Bellevue forward lookup
  returned eight results with five components each; reverse at the first point
  returned that one address with the same components and zero distance.
- Live Places and geocoding suites passed on baseline, activated candidate and
  restored baseline. Candidate fingerprint `9b21afce…` and rollback fingerprint
  `b76ee297…` were checked; neither database was rewritten.
- Installed Codex in-app browser: Bellevue lookup still presents eight candidates
  without automatic selection. Selecting the first displays its existing ID,
  coordinates, map marker and approximate precision. No frontend dependencies
  or tooling changed.
