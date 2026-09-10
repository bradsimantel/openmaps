# National Scout routing

The national workflow uses the sole [Go Scout backend](routing-scout.md).
The frozen 83-case qualification covers representative coordinates in all 50
states/DC, rural and long routes, Canadian/Mexican road legs, Alaska, Hawaii and
Aleutian roads on both sides of the dateline. The [historical qualification](log/0034-national-scout-qualification.md)
pins its source packages, graph, landmark vectors and limitations; the
[Go migration record](log/0035-scout-go-migration.md) records subsequent checks.

The selected 647-package generation is not a complete planet inventory. Complete
manifest handling, catalog omissions and explicit dependencies remain part of
acquisition. Positive island checks supplement dependency closure. Missing source
tiles remain incomplete data; ferries and address routing are unsupported.
Known source geometry defects and missing references outside the target remain
visible. Representative passing routes do not certify every source road.

The [historical PBF/SQLite capacity preflight](log/0026-national-routing-preflight.md)
measured a superseded pipeline. That routing pipeline, its capacity estimator and
its regional configuration files have been removed; downloaded datasets and
historical evidence are retained.
