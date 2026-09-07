# Newport refresh verification

2026-09-07 · Refresh milestone working tree based on `72440e1`.

Historical results for the new refresh workflow. Current commands and behavior
are in [the maintained workflow](../refresh.md); source findings and identity
review are in [0004](0004-newport-refresh-investigation.md).

## Chrome plugin acceptance remains blocked

Read README, AGENTS and prior logs, started the existing demo with
`go run ./cmd/server`, and attempted the available Codex browser plugin connection
before refresh implementation. The available execution tool rejected the call
before JavaScript or browser connection with:

```text
js: codex/sandbox-state-meta: missing field `sandboxPolicy`
```

A later minimal execution retry failed identically. Chrome tabs, autocomplete UI,
details UI and rendered map placement could not be inspected in this session.
No standalone Playwright was installed and no npm tooling was added. This is an
unfulfilled browser acceptance check, not a passing browser verification. The
prior standalone-browser evidence in [0003](0003-first-milestone-verification.md)
remains historical and is not evidence for this refresh.

## Data and rebuild verification

- Initial Go acquisition of pinned Overture July Places, Addresses and Divisions
  succeeded. Catalog and PBF hashes were enforced; the new regional export hashes
  were established in a separate source lock.
- Offline preparation reproduced bundle SHA-256
  `7c88ae8d951b4d0a8a7b3c417e78b45bde10153b8ddcb0373e37065f96df1eeb`.
- A second independent Go acquisition into
  `data/newport-2026-07-22-verified/`, without `-write-lock`, verified all source
  checksums and reproduced the same normalized bundle hash.
- The initial unmapped candidate was retained locally for inspection. The
  reviewed candidate includes the Pearl Car Wash mapping and retains seven
  unresolved possible identity pairs as distinct entities.
- The full comparison has no validation violations: 11,451 continuing public
  IDs, 40 additions, 151 absences, 806 changed entities, 84 added relationships
  and 107 removed relationships. Dataset direction is August → July.
- `TestRefreshRebuild` rebuilt from the archived bundle, baseline and replacement
  file and compared all logical rows: 11,491 entities, 11,491 source records,
  37,442 winning provenance rows, 1,161 relationships, six metadata records and
  11,491 FTS rows were identical. This includes raw source JSON, permanent
  mappings and absent-source identity history. SQLite byte identity is not the
  reproducibility contract.

## Running demo activation and rollback

Initialized `data/deployment.json` with the untouched original baseline and
started the server on `127.0.0.1:8080` with `-deployment`. Recorded the review
against the exact comparison fingerprint, activated the reviewed July candidate,
checked health and HTTP lookup behavior, then rolled back and checked again.
No database file was replaced in place. The cycle was repeated with the final
server build after the complete index-content validator was added.

| Artifact | SHA-256 |
| --- | --- |
| Original August baseline | `b76ee297a476a67ed9883c1d5f8fb6ede17e5c4fba5525a3677600be824e3d13` |
| Reviewed July candidate | `9b21afcefbd447ce5416ba86c4c32f44bdfde3904bc47a2567f44cff725bbc29` |
| Reviewed comparison | `ddf50b1fbb5ccb5dae455c7d4cdcf44b61e96e9bdba47738c2ae38e44a1e058b` |

Health reported the candidate fingerprint after activation and the exact original
baseline fingerprint after rollback. The running demo was left on August, with
July retained as the previous selection. The original source locks, baseline
bundle checksum, baseline mapping file and SQLite bytes were retained. An explicit
absence check for The Bunker (`om_79d5884baaffca2a4c87d49ca70c4bd5`) returned
HTTP 404 / `NOT_FOUND` on July and HTTP 200 with its original ID/name after
rollback. It did not redirect to the possible Buskers counterpart.

The opt-in Go `TestLiveDemo` passed before activation, on July and after rollback.
Each run checks all returned suggestions through details, coordinate presence
and ranges, IDs, expected first kinds/IDs, and a consistent dataset fingerprint
across requests. It checks eight inputs and resolves 17 suggestions per run.

| Input | First result in both snapshots |
| --- | --- |
| White Horse | White Horse Tavern |
| 50 Bellevue | Standalone 50 BELLEVUE Avenue, ahead of Redwood Library |
| Thames | Thames Street |
| Newport | Newport area |
| Redwood Library | Redwood Library and Athenaeum |
| 26 Marlborough | Standalone 26 MARLBOROUGH Street, ahead of White Horse Tavern |
| zzxnoresult | No suggestions |
| Pearl Car Wash | Pearl Car Wash with the already published August public ID |

White Horse retains `om_a5e3dc7692e4d3b90b71b94fba66ec5b` at latitude
41.49138952, longitude −71.31373108. The standalone Bellevue address retains
`om_f88c096070879478ee02e036d3e67480` at latitude 41.48654393, longitude
−71.30830418. Pearl retains `om_ef8f5f0dc3fcde9f13af6cbdce15c271` despite the
Overture key replacement. These are verified API coordinates, not newly
verified rendered marker positions.

Lower-ranked results were also reviewed: July's fifth `Thames` suggestion is a
second distinct Thames Street Kitchen ID where August returns Thames Street
Cigar. The first four IDs agree. The workflow exposes this difference; it does
not silently deduplicate businesses or claim unchanged ranking throughout the
result set. No coverage or accuracy score is inferred from eight queries.

Local evidence, excluded from Git, includes `report.json` (unmapped),
`reviewed-report.json`, `review.json`, `http-baseline.txt`, `http-candidate.txt`
and `http-rollback.txt` under `data/newport-2026-07-22/`.

## Routine checks and failure behavior

`gofmt -w cmd internal`, `go test ./...` and `go vet ./...` passed. Routine tests
use small deterministic fixtures and do not require external sources or a live
server. The regional rebuild and HTTP checks use the explicit `integration` tag
and named test selection.

New fixture tests verify:

- Repeated reviewed replacements retain the original anchor and relationships.
- Unreviewed remapping, cross-kind reuse, absent-source kind reuse, missing
  evidence, surviving-old replacements and many-to-one/one-to-many decisions fail.
- Possible splits and merges surface in review while identities stay distinct.
- Unexpected search results and missing FTS rows fail validation.
- Activation requires an intact review, matching database fingerprints and a
  recomputed report. Missing reviews, changed reports/databases/baselines,
  forged report assertions and concurrent-writer locks leave selection unchanged.
- A live handler switches content while preserving a continuing entity's ID,
  restores its old response on rollback, and retains its working database when a
  new selection cannot open, with HTTP 503 degraded health.
- Initialization cannot overwrite an existing deployment; rollback without a
  previous snapshot or with a missing previous database leaves selection unchanged.

Scheduling remains deferred. Browser acceptance remains blocked by the plugin
execution error described above. No commit was made for this milestone.
