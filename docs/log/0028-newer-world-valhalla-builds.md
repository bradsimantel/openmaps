# Newer downloadable world Valhalla builds

Date: 2026-09-09. Scope: follow-up source investigation to the historical
[small Go prototype investigation](0027-prebuilt-valhalla-feasibility.md), at
repository revision `a87aa40cb8292514d5e6fc83dd272817dfac75bf` with its uncommitted
prototype preserved. No engine changes, purchases, national downloads, commits,
or deployment changes were made in this follow-up.

## Finding

The December 2022 Internet Archive export is **not the newest available world
graph**. Two substantially newer sources are now verified:

| Source | Latest verified date | Access | Evidence and qualification |
| --- | --- | --- | --- |
| OSM Scout Server / modRana | Internal package timestamp June 20, 2026; published June 21 | Public anonymous downloads | Planet-derived graph distributed as 2,930 small packages. A downloaded 88,022-byte sample contains genuine Valhalla 3.4.0 tiles. |
| Interline Tilepacks | Built September 8, 2026, from September 7 OSM | Paid graph downloads; public metadata | Catalog identifies planet build 1965, Valhalla 3.8.3. Five consecutive daily builds are visible. No graph payload downloaded. |

The best free lead is OSM Scout Server. It is suitable for the next bounded
compatibility experiment. Neither source has been validated with our Go router
in this follow-up, and this finding does not establish national performance.

## OSM Scout Server: public planet packages

The project points its distribution builder at
[`data.modrana.org/osm_scout_server`](https://data.modrana.org/osm_scout_server/).
The live [catalog](https://data.modrana.org/osm_scout_server/countries_provided.json)
selects `valhalla-34`. Direct inspection on September 9 found:

- 439 region entries with Valhalla package lists, all carrying timestamp
  `2026-06-20_07:12` and package schema version `2`.
- Coverage entries across Europe, Russia, North America, Africa, Asia, South
  America, Central America, and Australia/Oceania. Examples include California,
  Japan, Algeria, Brazil, New Zealand, and Bremen.
- The [package directory](https://data.modrana.org/osm_scout_server/valhalla-34/valhalla/packages/)
  lists 2,930 `.tar.bz2` files. The union of regional lists references 2,896
  packages; 34 directory packages are not selected by those lists. A complete
  mirror must not assume that union is an exhaustive planet manifest.
- Bremen selects packages `144` and `1985`, with catalog compressed size
  43,494,668 bytes. These were not downloaded in this follow-up.

The [packaging code](https://github.com/rinigus/osmscout-server/blob/aae442c5d12345366f677729a004cb00a8115530/scripts/import/postprocess/valhalla_packs.py)
groups files from one planet tile directory, rather than rebuilding each country
separately. Region records reference shared package IDs. This is strong evidence
for a coherent planet-derived distribution, but cross-package routing and
completeness still require verification. The inspected source revision is
`aae442c5d12345366f677729a004cb00a8115530`; it is not an attested revision of the
June production build.

The [project README](https://github.com/rinigus/osmscout-server#readme) describes
map updates roughly every two to three months and credits hosting through the
modRana repository at Masaryk University. This is an approximate community
cadence, not a delivery guarantee. The live directory confirmed June files;
its changing `digest.new` timestamp is not evidence of a newer graph.

### Actual sample verification

Downloaded only
[`0.tar.bz2`](https://data.modrana.org/osm_scout_server/valhalla-34/valhalla/packages/0.tar.bz2),
88,022 bytes, SHA-256
`ffa34c77a64e9001f0099fd972c21c776d3491545820cc00b18a40341743c58e`.
It contains:

| Tile | Gzip member size | Decompressed tile size | Header version | Dataset ID |
| --- | ---: | ---: | --- | ---: |
| `0/000/747.gph` | 60,182 | 136,536 | 3.4.0 | 183131145 |
| `0/000/748.gph` | 26,822 | 60,192 | 3.4.0 | 183131145 |

The archive also contains a file list and an internal tile timestamp
`2026-06-20_07:12`. The external `0.tar.timestamp` says `07:13`, consistent with
separate packaging metadata. Neither timestamp proves the exact OSM replication
cutoff: the packaging script writes the current time. Do not report June 20 as
an independently verified OSM snapshot date. Dataset ID is also not a date.

`valhalla-34` is a distribution namespace, `2` is its package schema, and `3.4.0`
is the sampled tile header's producer version. These must not be conflated.
The previous prototype was pinned to 3.6.3, so header inspection alone does not
prove reader compatibility with these older tiles.

The tar contains nested `.gph.gz` members. A reader can use bounded per-tile
decompression, or preparation can decompress one package at a time into an
uncompressed tar suitable for random reads. No OSM graph construction is needed.
Before adopting it, verify binary layouts, available attributes and restrictions,
all hierarchy levels, cross-package references, source freshness, checksums,
snapshot consistency, and the provider's intended bulk distribution arrangements.

## Interline: current paid planet builds verified in the catalog

The anonymous [catalog endpoint](https://app.interline.io/valhalla_planet_tilepacks.json)
returned seven records. The newest five have creation dates September 4 through
September 8, 2026, each using Valhalla 3.8.3. Latest record:

```text
tilepack ID:                    1965
created_at:                     2026-09-08T20:58:10.966Z
osm_planet_datetime:            2026-09-07T11:00:00.000Z
valhalla_version:               3.8.3
interline_planetutils_version:  v0.4.14
tile cutter version:           v2.0.0
data_contents:                 openstreetmap, elevation
storage object:                valhalla-tilepacks / 2208/tiles.tar.gz
```

This establishes recent build availability beyond a marketing claim of daily
updates. It does not validate the payload or compatibility with our reader.
[PlanetUtils](https://github.com/interline-io/planetutils) exposes metadata listing
and authenticated download operations. The [published plan](https://www.interline.io/valhalla/tilepacks/)
is $800/month month-to-month, or $624/month with a prepaid annual contract.
No account was created, no purchase made, and no paid download attempted.

## Other leads checked

- [OpenPlanetData's Valhalla repository](https://github.com/openplanetdata/openplanetdata-valhalla-tiles)
  currently contains only a license, with no downloadable releases. Its
  [OSM dataset page](https://openplanetdata.com/datasets/openstreetmap/) provides
  source-data formats, not an advertised prepared routing graph. This is a lead
  to revisit, not a verified tile source.
- [Cardinal/Murena Maps](https://gitlab.e.foundation/e/os/maps) has public
  documentation for building and serving planet routing tiles for offline use.
  This investigation did not establish a dated, supported world download catalog.
- [Rati](https://github.com/valhalla/rati) serves individual Valhalla tiles from
  remote tar archives. It is relevant access software, not itself a verified
  provider of a world dataset.
- Librescoot remains a useful current regional fixture source. Other GitHub
  offline-routing repositories found in searches were regional builders,
  runtime implementations, or insufficiently documented distribution leads.
  They did not establish an additional coherent downloadable world release.
- Internet Archive title searches for Valhalla plus planet/routing returned the
  known 2022 tile export and later source-code archives, not a verified newer
  planet graph. A recently archived source repository is not a recent tile build.

## Reproduction and next experiment

Metadata and the tiny sample are saved under ignored
`data/valhalla-world-search/`. These commands fetch metadata and one small
package only; they do not download a national or world graph:

```sh
mkdir -p data/valhalla-world-search
curl --fail --location --max-time 60 \
  https://data.modrana.org/osm_scout_server/countries_provided.json \
  -o data/valhalla-world-search/modrana-catalog.json
curl --fail --location --max-time 60 \
  https://data.modrana.org/osm_scout_server/valhalla-34/valhalla/packages/0.tar.bz2 \
  -o data/valhalla-world-search/modrana-0.tar.bz2
curl --fail --location --max-time 60 \
  https://app.interline.io/valhalla_planet_tilepacks.json \
  -o data/valhalla-world-search/interline-catalog.json
shasum -a 256 data/valhalla-world-search/modrana-0.tar.bz2
python3 - <<'PY'
import gzip, struct, tarfile
with tarfile.open('data/valhalla-world-search/modrana-0.tar.bz2', 'r:bz2') as t:
    for m in t:
        if m.name.endswith('.gph.gz'):
            data = gzip.decompress(t.extractfile(m).read())
            version = data[16:32].split(b'\0')[0].decode()
            dataset = struct.unpack_from('<Q', data, 32)[0]
            print(m.name, len(data), version, dataset)
        elif m.name.endswith('/timestamp'):
            print(m.name, t.extractfile(m).read().decode().strip())
PY
```

The observed catalog SHA-256 was
`770e6c599cfb42d4f35cbe8b7b5be0295dae45d2d25ebe93ce3ccc9c23eca362`.
The live URLs can change; pin downloaded bytes before using them as fixtures.

Recommended next work is a small adjacent-package OSM Scout compatibility test:
adapt packaging access and version validation, exercise the existing Go search
across a package boundary, then audit restrictions and graph completeness.
That is a proposed next experiment, not a completed migration or benchmark.

Verification for this follow-up consisted of live metadata inspection, actual
sample decompression and header inspection, and documentation content/formatting
review. No Go code changed, so unrelated code tests were not rerun.
