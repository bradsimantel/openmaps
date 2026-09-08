# OpenAddresses September 6 US Northeast bundle inspection

2026-09-07 (America/Los_Angeles; inspection timestamps are September 8 UTC) ·
Revision `1c867e065d77d50c5dc990224891addb2b4da30b`, investigation only.

Historical follow-up to [0013](0013-address-source-comparison.md), prompted by
an actual US Northeast ZIP downloaded by the user after creating an OA account.
No lookup behavior, snapshots, activation state or public IDs changed.

## Finding and recommendation

**This September 6 bundle contains no Rhode Island source and no address points
in our Newport preview rectangle.** Its recent collection date is real. It does
not supply a newer RI replacement for the separately downloaded October 2024
output evaluated in 0013.

Retain the earlier **Newport-specific** recommendation: Overture remains the
lookup baseline; among the inspected sources, direct statewide E911 supplies the
Bellevue structure descriptions that OA's published RI output and NAD omit.
This is not a recommendation to reject OA generally or a conclusion based on
failure to obtain OA data. Both the source and validated RI outputs were obtained
in 0013, and this follow-up inspected the user's authenticated collection download.
The Massachusetts bundle member has a September 4 processing job and actual unit
values, illustrating why OA quality and usefulness need evaluation per source.

## What was inspected

Original archive: `collection-us-northeast.zip`, inspected from the project root
and subsequently deleted by the user. Its inventory, checksums and generated
evidence remain in ignored `data/investigations/oa-northeast-bundle/`; the original
ZIP bytes are no longer retained locally.

- Actual archive size: **1,770,504,523 bytes**.
- SHA-256: `817ca571de3bd44a0613ebf19ca70d1c5d42a537d11a553f3171c39c75122a2c`.
- **180 members:** 90 GeoJSON outputs and 90 accompanying `.meta` files.
- Output layers: **63 addresses**, 8 buildings, 5 centerlines, 14 parcels.
- Present source states: CT (25 outputs), MA (7), ME (4), NH (9), NJ (10),
  NY (27), PA (8). These counts include all layer types.
- No filename or metadata source names for RI or VT.
- Every address member was streamed and parsed: **19,337,871 point records**,
  **zero** within inclusive WGS84 longitude/latitude rectangle
  `[-71.33, 41.47, -71.29, 41.51]`. This is a count of source rows, not deduplicated
  addresses. No non-point address geometries occurred.
- Reading every address member to EOF verified its ZIP CRC; all metadata CRCs
  were also checked. Non-address geometry members were inventoried but not parsed
  or individually CRC-checked. The whole-archive SHA-256 covers their bytes.

The geographic scan guards against relying solely on state filenames: it searched
all address records, including the Massachusetts statewide member whose bounds
overlap our rectangle. There are no Bellevue records to compare in this bundle,
and no additional Newport benchmark candidates or enrichment values.

## Collection date versus constituent jobs

The live [official collection catalog](https://batch.openaddresses.io/api/collections)
lists collection `2`, `us-northeast`, created **2026-09-06T13:40:34.036Z**.
Its source patterns include both `us/ri/**` and `us/vt/**`, along with the seven
states present in the ZIP. All ZIP member timestamps also fall on September 6
(ZIP timestamps do not specify a timezone).

Configuration therefore includes RI, but the actual supplied output omits it.
The catalog reports 1,770,488,351 bytes, **16,172 fewer** than the supplied ZIP.
The reason is unverified; catalog size is not used as proof of byte identity.
The archive hash above identifies the exact evidence inspected.

| Source | Actual address rows in this ZIP | Embedded successful job / creation date (UTC) | Interpretation |
| --- | ---: | --- | --- |
| RI statewide | Absent | No member metadata | September 6 collection creation establishes no RI refresh |
| NY statewide | 5,583,821 | 898675 / August 28, 2026 | Recent processing; source freshness still requires upstream evidence |
| MA statewide | 2,732,597 | 904349 / September 4, 2026 | Recent processing, including actual unit values |
| NH statewide | 617,065 | 903656 / September 4, 2026 | Recent processing |
| ME statewide | 774,216 | 728122 / December 12, 2025 | Older job packaged in the new collection |
| NJ statewide | 2,847,736 | 488124 / November 15, 2024 | Older job; member count differs from job metadata |
| CT statewide | 1,182,356 | 443139 / August 30, 2024 | Older job packaged in the new collection |

Across all 90 metadata files, job creation dates range from August 21, 2020
(Hartford addresses) to September 4, 2026 (Bridgeport parcels). These are processing
dates, not survey dates. The collection is neither uniformly old nor uniformly
refreshed at its September 6 build time.

Two members have fewer parsed rows than their embedded job `count`: MA statewide
2,732,597 versus 3,710,635; NJ statewide 2,847,736 versus 3,698,774. Neither count
equals the corresponding metadata's `validity.valid` count (3,703,678 and
3,673,831). All other 61 address member counts match their metadata. The affected
members parse completely and pass their ZIP CRCs. This establishes a packaging/
job-metadata discrepancy, not its cause or which rows were omitted. Do not treat
metadata source counts as measured collection coverage or assume this ZIP is a
byte-for-byte copy of each full job output.

A fresh request to the [RI source catalog](https://batch.openaddresses.io/api/data?failing=true&source=us/ri/statewide)
still lists successful artifact **475152**, updated October 25, 2024, and latest
job **903253**. The latter's September 4, 2026 failure was recorded in 0013.
The separately downloaded successful output contains 440,571 statewide source
features, including all eight Bellevue points, but no useful populated source
IDs, units or structure descriptions. Its availability and the omission from this
collection are separate observations. The collection's omission rule is unresolved;
the failed latest RI job alone does not prove why the packager excluded it.

## Representative record and implications beyond RI

An actual Massachusetts member record, retained in `ma-unit-example.json`:

```json
{
  "hash": "d5b445969adf5bb0",
  "number": "1106", "street": "SANDWICH ROAD", "unit": "1",
  "city": "BOURNE", "district": "BARNSTABLE", "region": "MA",
  "postcode": "02561", "id": "", "accuracy": ""
}
```

Its point is `[-70.5244004, 41.7684078]`; member
`us/ma/statewide-addresses-state.geojson`, job **904349**. This verifies that a
published OA collection can carry structured units and locality fields. It does
not establish address validity, durable identity, positional accuracy or whether
our Overture data outside Newport already contains the same information.

OA remains a plausible scalable supplement where actual published records improve
coverage or attributes. One collection reader can handle many jurisdictions; it
needs member coverage checks, field completeness checks, per-source attribution
and identity review. A collection-wide timestamp is insufficient for those checks.
The targeted RI adapter recommendation concerns missing useful RI descriptions,
not a need to build custom adapters for every county.

## Reproduction and limits

Temporary Python inspection and acquisition scripts are not retained in the
repository. Inspection read the ZIP without extracting it, streaming every
address member and applying the inclusive coordinate bounds above. The scripts
are no longer available to rerun.
`inspection.json` retains complete member inventory, raw metadata, address counts,
decompressed address-member hashes, archive hash, script hash and any Newport
matches. `evidence-manifest.json` records generated evidence checksums and the
user-supplied acquisition provenance. The two live catalog responses have URL,
retrieval time, response headers and checksum sidecars, acquired separately from
the offline inspection. No account credentials or signed
download tokens were read or stored.

Verified: complete address scan, missing RI/VT members, zero Newport points,
September 6 collection metadata, mixed embedded job dates, and a populated MA
unit example. Unresolved: why RI/VT were omitted, catalog-size discrepancy,
MA/NJ row-count discrepancies, and upstream freshness of the other states. No
general completeness or quality assessment of those states was attempted.

Content/formatting review, inspection-output consistency checks and
`git diff --check` passed. The offline inspection script was subsequently removed;
no Go changes or network-dependent routine tests were introduced.
No commit made.
