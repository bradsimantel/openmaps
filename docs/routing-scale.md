# Routing storage and scaling

The supported routing architecture is [Scout](routing-scout.md): immutable tile
pages, prepared turn indexes, directed landmarks, bounded reader caches and Go
search. Lookup imports still use SQLite and PBF street extraction independently.

The former Oregon/Northwest PBF graphs, junction-cell overlays, mapped caches and
SQLite prepared formats have been retired. Their measurements remain historical
in `docs/log/0019` through `docs/log/0026`; they do not describe current serving or
construction capacity. See the [historical national Scout qualification](log/0034-national-scout-qualification.md)
and [Go migration verification](log/0035-scout-go-migration.md).
