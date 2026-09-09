# Eastern Aleutian source extension

Date: 2026-09-09. Scope: uncommitted Scout implementation based on `d24c604`.
Historical investigation; maintained behavior is in [Scout routing](../routing-scout.md).
This extends [0032](0032-national-closure-and-reverse-edge-identity.md).

## Why reference closure was insufficient

The 643-package graph had no level-2 tiles at positive longitudes 170–180°, latitudes
50–55°. A missing-reference audit could not reveal disconnected networks absent
in their entirety. Positive Attu/Shemya tile probes found this gap after the
81-case national route and HTTP suite had passed. The original evidence remains
in `data/scout-national-20260909/positive-aleutian-tiles.json` and the empty
`positive-aleutian-roads.jsonl`; the gap was not treated as verified road absence.

The provider's Kamchatka selection contains four previously unacquired packages:
2287, 2435, 2676 and 2929, totaling 19,469,654 compressed bytes. Package 2676
contains the eastern-hemisphere Aleutian level-2 tiles, including Attu 823652 and
Shemya 822216. The full pinned catalog/digest/directory generation was checked
before and after acquisition and remained unchanged. Catalog membership is not
jurisdiction or an exhaustive tile inventory. The 647-package selection totals
9,192,382,849 bytes, from the 2,930-package manifest.

`aleutian-discovery-inventory.json` records archive members; the actual graph
probe retained 638 eligible directional road records in
`positive-aleutian-roads-647.jsonl`. Endpoints were selected from stored shapes:

| Source-profile case | From lon, lat | To lon, lat | Road metres |
| --- | --- | --- | ---: |
| Shemya | 174.082093, 52.729287 | 174.150464, 52.710423 | 6,032.156 |
| Attu | 173.189465, 52.838092 | 173.169631, 52.880188 | 7,731.384 |

Both paths passed the independent raw-source path verifier and ordinary Dijkstra
cost comparison. This is evidence about the experimental encoded-access profile,
not a claim that a civilian can enter military property. Raw-source access and
entrance evidence discarded by provider normalization cannot be recovered.

## Bounded extension and immutable ownership

The Go preparation extension verifies the old graph and exact retained package
pins, clones its spool with macOS copy-on-write into a distinct inode, then
imports only the four additions. All old tile payload hashes remain identical.
The new receipt binds the prior receipt. The serving files were never appended
through hardlinks or selected while under construction.

| Artifact | Value |
| --- | --- |
| Tiles | 37,362 (3,062 added) |
| Nodes | 122,732,001 (197,292 added) |
| Spool bytes | 24,522,784,768 |
| Spool SHA-256 | `9e6c5973e546615150100dc15eaa5ec9a79ad2c95749cf369391edf6e91ca22e` |
| Receipt SHA-256 | `4cb6be091e5a7751d77d45d5ae719849e29971af6d5677f63e35e71abafda2a1` |
| Base receipt SHA-256 | `019cf0813d9187f317a25456c1f6df6909688cb94bf830df0ff53a30964c9bf6` |

Graph extension took 34.133 seconds, 106,610,688 bytes sampled peak RSS, and
approximately 60.7 MB observed physical free-space change. The 24.5 GB logical
spool size is not additional physical storage on this copy-on-write filesystem.
Ordinary-copy mode remains available when a filesystem lacks cloning and has
sufficient headroom. Independent synthetic copy and clone tests compare against
a fresh full import and check that the original bytes/inode remain separate.
The earlier 643-package graph was independently reimported from all compressed
inputs; this latest extension is explicitly a verified reuse, not another full
independent 647-package decompression.

## Landmark reindex proof

All old tile bytes were unchanged. A full scan of ordinary permitted directed
edges and hierarchy transitions found **zero** connections crossing the old/new
tile partition. A conservative extension check would reject even one connection
touching any finite old landmark component, requiring full recomputation.

Consequently the old finite distances are unchanged and all added nodes have
infinite distance to/from the nine existing landmarks. The implementation copies
old vector blocks into the new dense node order and fills added blocks with
infinity. It publishes a checksummed proof binding the old/new graph receipts
and old landmark manifest, plus new per-vector receipts and final manifest.
Finite-component connection rejection and proof corruption are tested. A fixture
inserts a disconnected tile before existing nodes in dense order, then compares
every resulting vector byte with independently recomputed Dijkstra vectors.

`national-aleutian-reindex.resources.json` records 83.342 seconds and 273,088,512
bytes sampled peak RSS, no budget abort. Free disk decreased from 46,707,507,200
to 37,870,338,048 bytes as the eighteen vectors were written. Graph potential
preparation overlapped part of this run; these are shared-host observations.
Construction and serving retain the 32 GiB free-disk reserve and 4 GiB sampled
per-process RSS supervision. Sampling and Go soft memory targets are not hard
RSS or total filesystem-cache limits.

The full expanded source audit, frozen 83-case qualification and HTTP replacement
are recorded in [0034](0034-national-scout-qualification.md), including failures
outside the target and remaining uncertainty about exhaustive source coverage.

The final resume recheck additionally hashed each retained block from both owned
old/new graph files against the tile pins, guarding against internally inconsistent
receipts. It verified all existing reindexed vectors without mutation in 103.420
seconds at 263,733,248 bytes sampled peak RSS, under concurrent HTTP traffic
(`national-aleutian-reindex-payload-recheck.resources.json`).
