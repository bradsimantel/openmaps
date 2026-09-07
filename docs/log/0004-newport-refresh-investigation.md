# Newport refresh sources and identity decisions

2026-09-07 · Refresh milestone working tree based on `72440e1`.

Historical investigation and decisions for the second Newport snapshot. Current
operation and rules are maintained in [the refresh workflow](../refresh.md).
This supersedes the first milestone's deferral of refresh reconciliation in
[0002](0002-newport-data-and-import-design.md); it does not change its original
snapshot findings.

## Release selection and official references

The baseline uses Overture **2026-08-19.0**, already the latest published release
on the investigation date. The available second release is **2026-07-22.0**.
Both are listed in the [official release archive](https://docs.overturemaps.org/blog/archive/)
and [release notes](https://docs.overturemaps.org/blog/). July's notes identify
schema v1.18.0. August's notes identify the same schema version. No later release
was invented or silently substituted.

This is an August → July historical rehearsal followed by rollback to August,
not a freshness improvement. Places, Addresses and Divisions use the July pin;
Geofabrik streets remain **2026-08-01** to isolate Overture changes. The region,
expanded division-parent sample, parsing and basemap are unchanged. The
[Geofabrik regional source page](https://download.geofabrik.de/north-america/us/rhode-island.html)
was also checked; no latest PBF URL was introduced.

The July STAC catalog was acquired with Go from
`https://stac.overturemaps.org/2026-07-22.0/collections.parquet`; SHA-256:
`144bc5bda078e783ba68dec077b7e75890c4aa1b6404cf488fc0002a414ba786`.
The named source lock and bundle checksum are in `imports/`. The established
maintainer `prepare -write-lock` path created the new canonical export hashes.
Normal offline preparation and an independent fresh Go fetch then verified these
pins without `-write-lock`. No large datasets were added to Git.

[Overture's GERS overview](https://docs.overturemaps.org/gers/) describes stable
identifiers and the registry, bridges and changelog that support them. The
[official changelog guide](https://docs.overturemaps.org/gers/changelog/) defines
added, removed, changed and unchanged records between consecutive releases.
Those states do not establish replacement equivalence. We compare complete
regional snapshots rather than assume an ID hash solves churn or interpret a
regional absence as a physical closure. The upstream changelog and registry were
not downloaded for this rehearsal; original source properties provided the
specific replacement evidence below.

## Observed changes

All directions below are **August baseline → July candidate**. In chronological
July → August terms, the addition/removal columns would reverse.

| Measure | August | July |
| --- | ---: | ---: |
| Businesses | 2,173 | 2,062 |
| Addresses | 8,545 | 8,545 |
| Areas | 4 | 4 |
| Street ways | 880 | 880 |
| Entities/source records | 11,602 | 11,491 |
| Explicitly closed businesses | 51 | 41 |
| Business/address relationships | 1,181 | 1,158 |
| Parent-area relationships | 3 | 3 |
| Winning attribute-provenance rows | 37,943 | 37,442 |

Before reconciliation, 11,450 IDs continued, 41 were added and 152 disappeared.
After the one reviewed replacement below, **11,451 public IDs continue, 40 are
added and 151 are absent**. All additions and absences are businesses. Address,
area and street identities and public attributes are unchanged.

**806 continuing businesses** change at least one public attribute. Field counts
overlap: 753 addresses, 96 locations, 83 websites, 50 names and 16 closed flags.
Many address changes involve formatting or ZIP+4, but not all: Trinity Church
changes from `1 Queen Anne Square` to `44 Church St` in this comparison. Source
strings are retained rather than normalized for display or presumed more correct
because one snapshot is later.

Among continuing source keys, 10,570 change release, 2,025 change raw JSON, 806
change normalized attributes and 21 change attribute paths. These categories
overlap. Release changes alone are not public attribute changes. The report
lists source keys; retained databases contain their full source records.

Relationships have **84 additions and 107 removals**; unchanged parent links
remain. Links are recomputed from the candidate's number/street/postcode and
50-metre uniqueness evidence. Identity preservation does not freeze an obsolete
business/address relationship. The July adapter reports 76 ambiguous business
address matches versus August's 81, left unlinked.

## Pearl Car Wash: reviewed one-to-one replacement

The same business has different Overture keys:

- August: `overture:place:81291527-f09e-4b84-af71-61abf103e7be`.
- July: `overture:place:47b0ff6b-7eb4-469e-96f3-5978c3dcd5dc`.

Inspected raw source records agree on Meta `record_id` **115085711201621**,
primary name **Pearl Car Wash**, address **202 J T Connell Memorial Rd**,
postcode **02840-1037**, coordinates **longitude −71.31818885, latitude
41.50997468**, website `https://pearlcw.com/`, phone `+14016192274`, email and
Facebook URL. The two GERS IDs do not coexist in either regional snapshot.
This supports a specific one-to-one continuation; no nearest-name rule is applied.

The already published August anchor is retained. The July source resolves to
**`om_ef8f5f0dc3fcde9f13af6cbdce15c271`** through autocomplete and details.
Neither snapshot has a standalone-address relationship for Pearl; none was
fabricated during reconciliation. The proposed July bootstrap ID
`om_185af1e49296d616228d0d8822a455ed` is not published by the reviewed candidate.
The decision and evidence are retained in
[the replacement file](../../imports/newport-2026-07-22.replacements.json) and
candidate metadata. This does not modify the baseline source bundle or mappings.

## Seven uncertain pairs remain distinct

These are review leads, not established real-world split/merge events. No
mapping or redirect was added for them.

| August record → July counterpart | Evidence and reason to keep distinct |
| --- | --- |
| Discover Newport → Newport County Convention and Visitors Bureau | Same website/address, 48.33 m apart; August identity also survives in July, so a one-to-one replacement would be wrong |
| The Bunker → Buskers | Same website/site, 0.023 m apart; Buskers already exists in August; names differ |
| Parlor Newport → Parlor Bar and Kitchen | Same website and coordinates; counterpart already exists in August |
| Water Brothers → Water Brothers Surf & Skate | Same website and coordinates, but materially different address strings; identity evidence remains insufficient |
| Spring Street Newport RI → Spring Street Spirits | Same coordinates and Digs Design website, conflicting labels; shared possibly erroneous source attributes do not establish identity |
| Yacht Queen → B&B Yacht Charters | Shared charter-specialist URL, 12.27 m apart; counterpart already exists in August and addresses differ |
| The Newport Harbor Hotel and Marina → another ID with the same name | Same coordinates, website and underlying Meta ID; original identity also survives in July, so even strong attribute agreement does not authorize merging |

The detector compares added/absent entities with surviving counterparts, rather
than limiting review to an old/new pair set. It uses same kind, 50 metres, and
same normalized name or business website. It does not claim complete recall:
large moves or simultaneous name and website changes can be missed. The full
addition/absence lists remain reviewable independently of these leads.

The review accepted the historical candidate for a local activation/rollback
rehearsal with these seven unresolved pairs explicitly kept distinct. It is not
an approval to replace August with older data permanently or to assert geographic
coverage, real-world closures or duplicate resolution.
