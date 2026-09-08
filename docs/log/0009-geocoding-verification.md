# Newport geocoding verification

2026-09-07 · Geocoding milestone working tree based on `ee8a74c`.

Historical findings. Current API/algorithm limitations and reproducible benchmark
commands are maintained in [geocoding.md](../geocoding.md). Contract investigation
is in [0008](0008-geocoding-contract.md).

## Quality and source fidelity

The deterministic 26-case benchmark passed on the exact retained August baseline
and reviewed historical July candidate. Five HTTP-handler executions per case
checked statuses, ordered IDs, approximate location type, match precision,
partial-context behavior, coordinates and reverse distances. Expected source
numbers, street names, source keys and coordinates were checked directly against
25 original raw address records in **each** snapshot. Every expected ID resolves
through Places details to the same address and coordinates.

The benchmark's full snapshot reader also checked referential integrity, stable
anchors, source references and existing index content. Full comparison of address
entities found **8,545 identical, zero changed, zero absent, zero added**. Consequently
there are no material geocoding result differences between the two retained
snapshots, including order and ambiguity. No result IDs were remapped. The 138
nonstandard label exclusions are identical; 8,407 points are eligible in both.
These are source-fidelity findings, not ground-truth geospatial accuracy scores.

| Case | Expected and observed on both |
| --- | --- |
| `50 Bellevue Avenue` and `050 bÉlleVue AVE.` | One identical source point / public ID |
| Bellevue with Newport, RI, postcode, US context | Same point; partial context note, no inferred locality |
| `26 Marlborough St` | Standalone address ID, not the distinct White Horse business |
| `364 Bellevue Avenue` | Eight distinct candidate IDs, stable order |
| `199 James T Connell Memorial Rd` | Twelve distinct candidate IDs, stable order |
| Wrong house number, incomplete street, Boston/MA or wrong postcode | `ZERO_RESULTS`, no fallback |
| Apartment request, range, street alone, locality alone | Explicit `INVALID_REQUEST` |
| Exact Bellevue coordinates | Same address, 0 m |
| 41.48656, −71.30835 | Bellevue address, 4.215 m |
| 41.49136, −71.31374 | Marlborough address, 0.917 m |
| Water gap at 41.485, −71.329 | No supported point within 100 m (nearest is 148.582 m) |
| Just inside south at 41.470001, −71.32848162208666 | 5 Beacon Hill Road, 0.196 m |
| Just outside south at 41.469999, same longitude | Outside coverage, even though that point is 0.419 m away |
| North boundary at 41.51, −71.30549890898419 | 79 Park Holm Street, 0.599 m |
| East boundary at 41.50944932354806, −71.29 | 357 Valley Road, 1.189 m |
| One microdegree east of that boundary | Outside coverage despite nearby source point |
| 0,0 | Outside coverage |

Representative median handler times from five runs, excluding startup, database
loading and network: Bellevue forward ~6–7 µs; nearby reverse ~0.52–0.53 ms; water
gap ~0.52–0.53 ms. This small local timing sample supports keeping a full regional
reverse scan. It is not a load test, latency guarantee or production capacity claim.

## Snapshot activation and rollback

Used the existing retained comparison and review receipt. `refresh activate`
recomputed the comparison and accepted the unchanged reviewed July database.
`/healthz` and all live lookup response headers confirmed the selected fingerprint.
`refresh rollback` restored the original August fingerprint and result behavior.
No database, source lock or identity mapping was modified.

| State | SHA-256 |
| --- | --- |
| August baseline and restored state | `b76ee297a476a67ed9883c1d5f8fb6ede17e5c4fba5525a3677600be824e3d13` |
| Reviewed July candidate | `9b21afcefbd447ce5416ba86c4c32f44bdfde3904bc47a2567f44cff725bbc29` |

`TestLiveGeocoding` passed all 26 requests over real HTTP before activation, on the
candidate, and after rollback, with one consistent fingerprint per run. Existing
`TestLiveDemo` also passed in all three states (eight autocomplete inputs, every
returned suggestion resolved through details). Retained business changes remain
as documented in historical [0004](0004-newport-refresh-investigation.md); they
are not geocoding changes because businesses and addresses remain distinct.

Local outputs are retained outside Git as `data/geocoding-benchmark.txt` and
`data/geocoding-live-{baseline,candidate,rollback}.txt`. The running demo is left
on the original August baseline with July retained for future review/rehearsal.

## Installed in-app browser

Used the installed Codex browser plugin against `http://127.0.0.1:8080`, including
actual text entry, button/keyboard submission, candidate selection and map clicks.
The MapLibre/Protomaps map, labels, markers and screenshots were visually inspected.
No standalone Playwright, browser install or npm tooling was added.

- Baseline normalized Bellevue input produced the expected address and a visible
  marker beside Redwood Library at 41.486544, −71.308304.
- Clicking nearby returned that address, with **14.01 m** shown, the 100 m limit,
  separate query/source markers and explicit unknown rooftop/entrance/unit and
  containment precision. Forward/reverse Bellevue flows also passed on July.
- `364 Bellevue Avenue` displayed all eight separate IDs/coordinates, with no
  automatic marker. Choosing the second candidate displayed its own location
  (41.478658, −71.306828) and retained an ambiguity notice.
- `50 Bellevue Ave Apt 2` showed the unsupported-unit explanation and removed
  the preceding selection. A nonexistent house number showed no exact match and
  no fallback. A map click outside the rectangle showed outside coverage; a click
  in the western water gap showed no supported point within 100 m.
- Newport-qualified Bellevue input visibly showed the partial-context explanation
  without adding a source locality. Details scroll into view in the narrow panel.
- After rollback, Enter submitted `26 Marlborough St`; the original address ID
  and source coordinates appeared on the map. A subsequent map click returned
  `68 BROADWAY` at 41.491980, −71.311863, **13.97 m** from the clicked location.
- Final browser regression checked White Horse autocomplete → details → marker,
  then restored the Bellevue forward result with its visible source marker.
  The final server build also passed another complete live activation/rollback cycle.

No JavaScript errors were reported in inspected browser logs. MapLibre warned of
an unavailable `townhall` sprite from the existing basemap asset set; tiles and
lookup markers rendered. This basemap icon warning is unrelated to geocoding.
The default narrow viewport was verified. A temporary desktop viewport override
produced a clipped browser capture, so it was reset; this does not establish a
complete desktop responsive-layout verification. Source accuracy, building
containment, unit coverage and real-world deliverability remain unverified.

## Routine verification

`gofmt -w cmd internal`, `go test ./...`, `go vet ./...` and `git diff --check`
passed. Tests use small offline fixtures; real snapshot and live HTTP checks are
behind the explicit `integration` build tag. Meaningful additions cover:

- Leading-zero and letter-suffix numbers, accents, abbreviations, context and
  duplicate identity ordering; unsupported forms and explicit units.
- Finite/ranged coordinates, dateline distance math, cancellation, 99.99 m versus
  100.01 m, reverse ties and outside-rectangle queries.
- v3 envelopes, missing/duplicate/unsupported options, methods, bodies, field
  masks, missing components and conservative precision.
- Activation of a fixture whose address coordinates change, preserving its ID
  while loading the new point; rollback restores the old geocoding response.
  Existing failed-reload and Places behavior tests continue to pass.

Maintained README, geocoding and refresh documentation received content/formatting
review. Large data remains ignored, AGENTS.md was not changed, and no commit was made.
