# Open Maps

Open Maps is a project to build a drop-in replacement for Google Maps APIs using
Go and SQLite. The initial focus is places, geocoding, and routing. The client
will use MapLibre GL JS, with Protomaps providing the basemap tiles.

The project is at the design stage. No APIs are implemented yet.

## What we're building

Open Maps will let applications search for places and addresses, resolve them to
locations, and calculate routes between them.

| Area | Intended capabilities |
| --- | --- |
| Places | Autocomplete, place details, text search, and nearby search |
| Geocoding | Forward address lookup and reverse lookup from coordinates |
| Routing | Road snapping, route calculation, and directions |

Autocomplete searches across businesses, addresses, streets, and geographic
areas. Addresses are part of the places experience, even though forward and
reverse geocoding also have their own API operations.

## Compatibility

The goal is for existing applications to use supported Google Maps API operations
through Open Maps with minimal integration changes, such as configuring a new
base URL.

Compatibility includes request parameters, response shapes, status and error
behavior, and identifiers used across operations. Before implementing an
endpoint, we will identify its Google API version and document the supported
behavior. Matching API contracts does not imply matching Google's dataset,
ranking, or coverage.

Open Maps will issue its own stable identifiers. Existing Google place IDs are
not automatically interchangeable with Open Maps IDs. MapLibre GL JS is the
chosen map client; compatibility with the Google Maps JavaScript SDK is a
separate concern from HTTP API compatibility.

## Architecture

Start with one Go service and explicit packages organized around the operations
they implement:

```text
internal/
  places/       # Autocomplete, text/nearby search, details
  geocoding/    # Forward and reverse geocoding
  routing/      # Road snapping, graph search, directions
  api/          # Google-compatible handlers and orchestration
```

These are planned package boundaries, not an inventory of existing code.

### Places and geocoding

Places and geocoding share a SQLite dataset and can query the same records and
indexes. Their package boundaries describe behavior, not exclusive ownership of
entity types.

Keep each operation's queries and ranking logic in its owning package initially.
Extract shared retrieval or normalization code when a concrete need emerges, and
name it after the responsibility it implements.

Businesses, addresses, streets, and geographic areas retain distinct identities
and relationships. A business and its address are related entities; multiple
businesses may share an address. An autocomplete result should resolve
consistently through the supported details and geocoding operations.

### Routing

Routing is separate from places and geocoding in code and data. It operates on a
road graph with access rules, travel costs, and turn restrictions.

When a route request contains an address or place ID, the API layer resolves it
through places or geocoding, then passes the location to routing. Routing owns
snapping that location onto an accessible road. Use entrance or access-point
information when available.

Initially, these components can run in the same Go process. A separate routing
service is an option if deployment, memory, or scaling requirements justify it.
The routing engine and graph representation have not been selected.

### Map display and data

MapLibre GL JS renders the map using Protomaps tiles. The basemap is separate from
the searchable geographic records and routable road graph used by the APIs.

## Data sources

The proposed starting sources are Overture Maps for places, addresses, and
geographic areas, and OpenStreetMap for streets and routing. Validate these
choices against a small launch region before committing to coverage or quality
claims.

These sources are an initial foundation. Open Maps is intended to incorporate
additional datasets over time to expand coverage, improve accuracy and
freshness, and add richer attributes or new capabilities. The list below is not
exhaustive, and no entity type is permanently tied to a single provider.

| Data | Starting source | Use |
| --- | --- | --- |
| Businesses and points of interest | [Overture Places](https://docs.overturemaps.org/guides/places/) | Autocomplete, text/nearby search, and details |
| Address points | [Overture Addresses](https://docs.overturemaps.org/guides/addresses/) | Address autocomplete and forward/reverse geocoding |
| Named settlements and administrative areas | [Overture Divisions](https://docs.overturemaps.org/guides/divisions/) | Area suggestions, geographic context, and containment |
| Streets and road network | OpenStreetMap regional PBF extracts from [Geofabrik](https://download.geofabrik.de/) | Street lookup and a routing graph built from road geometry, access tags, and restriction relations |
| Basemap tiles | [Protomaps](https://docs.protomaps.com/) | Map display in MapLibre GL JS |

Overture Places alone is not an address database. Import standalone address
records and geographic areas as well as businesses so autocomplete can support
all of these entity types. Preserve the distinction between a place's associated
address and an independently sourced address point.

Before building the first importer, inspect regional samples for missing
addresses, duplicates, name variants, and field completeness. If coverage is
insufficient, evaluate local government address datasets or additional OSM data
for the specific gaps. Define matching and deduplication rules before combining
overlapping sources.

Keep source-specific parsing and mappings in import code, with API operations
working against Open Maps entities. An entity may have records from multiple
sources; preserve their source-qualified IDs and the provenance of contributed
attributes. Adding or replacing a source should not require changing the public
API or renumbering existing entities. Define explicit conflict-resolution and
merge rules as sources are integrated, while keeping the initial implementation
simple rather than building a general plugin framework in advance.

Pin source releases or extract timestamps and record download locations,
checksums, region bounds, source IDs, and attribution in an import manifest.
Review the [Overture source-specific licensing information](https://docs.overturemaps.org/attribution/)
and the terms for each additional dataset when defining the import and
distribution process.

Imports should be reproducible. Public identifiers must remain stable across
rebuilds rather than depend on transient SQLite row IDs. Import tooling and the
first supported region remain open decisions.

## Starting point

Build a complete flow for one region: search for a place or address, select a
result, display it on the map, and calculate a driving route to it.

1. Choose the region, validate the proposed data sources, and define the initial
   Google API compatibility targets.
2. Build a reproducible import pipeline and SQLite schema.
3. Implement autocomplete and details with a small map client.
4. Add forward and reverse geocoding.
5. Add routing and verify the complete flow.

Track correctness, search relevance, coverage, and latency with representative
regional examples. Expand the supported API surface and geography as those
results justify it.

Development and run commands will be documented when the implementation exists.
