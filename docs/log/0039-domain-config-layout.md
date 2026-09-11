# Domain configuration layout

Date: 2026-09-10

Scope: repository layout and lookup command configuration

The former top-level `imports/` directory mixed runtime source pins, historical
refresh inputs, identity state and routing qualification fixtures. It has been
removed rather than renamed as a unit.

The maintained runtime inputs are now the three domain-named files under
`config/`: `places-geocoding.json`, `routing.json` and `basemap.json`. Places
comparison expectations are compiled defaults rather than a separate JSON file.
Routing qualification cases, landmark seeds and snap probes now live with the
qualification code under `internal/routing/qualification/testdata/`. The July Newport refresh rehearsal
and Bremen Scout experiment live beside the historical records that explain
them in `docs/log/`.

The Places/geocoding config now carries the expected normalized bundle SHA-256 alongside
the pinned source releases and source checksums. This removes the two standalone
checksum sidecars. `prepare`, `import` and `refresh build` all read the same
config field. `prepare -update-config` is the explicit maintainer operation for
accepting reviewed source exports and the resulting bundle checksum.

The checked-in identity map was empty, so it has been removed. `prepare` still
accepts an optional `-identities` JSON file when a future source transition needs
explicit permanent mappings. This changes file organization and command flags;
it does not weaken source pinning, provenance retention or checksum enforcement.
