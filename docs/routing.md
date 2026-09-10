# Routing architecture and HTTP contract

Scout is the sole routing implementation. See the maintained [Scout contract,
profile, preparation and serving workflow](routing-scout.md).

Go decodes pinned Scout Valhalla 3.4.0 tiles, snaps coordinates, enforces retained
access/turn rules and returns full geometry with provider edge-speed estimates.
Ordinary traversal remains the correctness reference; national serving uses
one-sided directed landmark A*. The API layer owns Google request/response
translation, explicit masks, errors and metadata.

`POST /directions/v2:computeRoutes` retains its established Routes REST v2 subset:
coordinate origin/destination, `DRIVE`, `TRAFFIC_UNAWARE`, required
`GEO_JSON_LINESTRING`, optional `HIGH_QUALITY`; no extra body parameters. Response
masks support `routes.distanceMeters`, `routes.duration`, `routes.staticDuration`
and `routes.polyline.geoJsonLinestring` (including the supported polyline parent).
Broad `*` and `routes` masks are rejected. `key`, `fields` and `$fields` retain
existing parsing and duplicate rejection. Invalid or duplicate JSON, unsupported
fields and malformed coordinates return 400. API keys are not authenticated.

The profile remains `osm-scout-public-auto-v1`, cost model `scout-edge-speed-v1`.
It cannot recreate the retired `driving-time-v4` source semantics. Address
requests return 503 `address_routing_unavailable`; missing tiles remain 503
`incomplete_data`; unsnappable endpoints return 400; exhaustive unreachable
networks return 200 with empty routes. Budget exhaustion, cancellation, deadlines
and admission remain distinct. Route metadata identifies the snapshot, both
snaps, unverified gaps, attribution and source limitations independently of masks.

Lookup SQLite, basemap tiles and routing pages remain separate. The server can
serve all three together; routing snapshot replacement is independent of lookup
refresh and retains leases through response encoding. There is no PBF/SQLite
routing importer, overlay engine, mapped graph cache or legacy startup flag.

The historical investigations in [0015](log/0015-newport-driving-routing.md)
through [0034](log/0034-national-scout-qualification.md) describe prior milestones;
[0035](log/0035-scout-go-migration.md) records their migration to the sole backend.
