#!/usr/bin/env python3
"""Read-only routing capacity estimate; never downloads, builds or approves data.

Compressed PBF ratios are planning proxies, not graph counts or measured national
peaks. Inputs must identify their retained measurement evidence. The report uses
the worst regional ratio and an explicit contingency for each resource.
"""

import argparse
import datetime
import json
import math
import os
from pathlib import Path
import platform
import shutil
import subprocess

GIB = 1 << 30
METRICS = (
    "import_footprint_bytes", "prepare_footprint_bytes", "prepare_live_heap_bytes",
    "sqlite_bytes", "prepared_bytes", "sort_bytes", "import_seconds",
    "prepare_seconds", "startup_seconds", "nodes", "segments", "edges",
    "cell_paths", "ancillary_bytes",
)


def positive(value, name):
    if (isinstance(value, bool) or not isinstance(value, (int, float))
            or not math.isfinite(value) or value <= 0):
        raise ValueError(f"{name} must be a finite positive number")
    return value


def estimate(model, host, budgets):
    if type(model.get("schema")) is not int or model["schema"] != 1:
        raise ValueError("unsupported capacity model schema")
    contingency = positive(model["contingency"], "contingency")
    if contingency < 1:
        raise ValueError("contingency must be at least one")
    for name in ("ram_bytes", "free_disk_bytes"):
        positive(host[name], name)
    for name in ("construction_bytes", "disk_reserve_bytes"):
        positive(budgets[name], name)
    if budgets["construction_bytes"] >= host["ram_bytes"]:
        raise ValueError("construction budget must leave RAM for the host")
    baselines = model["baselines"]
    if not baselines:
        raise ValueError("at least one regional baseline is required")
    names = set()
    for row in baselines:
        if not row.get("name") or row["name"] in names or not row.get("evidence"):
            raise ValueError("regional names must be unique and evidence is required")
        names.add(row["name"])
        for metric in ("source_bytes", *METRICS):
            positive(row[metric], metric)
    results = []
    names = set()
    for source in model["sources"]:
        if not source.get("name") or source["name"] in names or not source.get("evidence"):
            raise ValueError("source names must be unique and evidence is required")
        names.add(source["name"])
        size = positive(source["source_bytes"], "source_bytes")
        values = {}
        for metric in METRICS:
            projections = [row[metric] * size / row["source_bytes"] for row in baselines]
            upper = max(projections) * contingency
            if not math.isfinite(upper):
                raise ValueError("projection overflow")
            values[metric] = {"regional_ratio_low": min(projections),
                              "regional_ratio_high": max(projections),
                              "planning_upper": math.ceil(upper)}
        upper = lambda key: values[key]["planning_upper"]
        db, artifact, scratch = (upper(k) for k in ("sqlite_bytes", "prepared_bytes", "sort_bytes"))
        # A single coherent source needs no merged PBF or staging database in
        # the existing importer. Future external staging needs a revised model.
        disk = {
            "acquisition": size,
            "import": size + db + scratch,
            "first_publication": size + db + artifact + scratch,
            "rebuild_and_old_new_overlap": size + 2 * (db + artifact) + scratch,
        }
        memory = max(upper("import_footprint_bytes"), upper("prepare_footprint_bytes"),
                     upper("prepare_live_heap_bytes"))
        reasons = []
        if memory > budgets["construction_bytes"]:
            reasons.append("projected construction exceeds declared process budget")
        if max(disk.values()) + budgets["disk_reserve_bytes"] > host["free_disk_bytes"]:
            reasons.append("projected lifecycle disk plus reserve exceeds free disk")
        # These are current construction/layout rejection limits, not promises
        # that national counts scale linearly with compressed source bytes.
        limits = {"nodes": 2147483647, "segments": 2147483647 // 2,
                  "edges": 2147483647, "cell_paths": 2147483645,
                  "ancillary_bytes": 128 << 20}
        for key, limit in limits.items():
            if upper(key) > limit:
                reasons.append(f"projected {key} exceeds current representation limit")
        results.append({"source": source, "estimates": values, "disk_phases_bytes": disk,
                        "construction_planning_bytes": memory,
                        "decision": "blocked" if reasons else "fits_estimate_only",
                        "reasons": reasons})
    if not results:
        raise ValueError("at least one national source is required")
    return {"schema": 1, "method": "regional compressed-PBF ratios with explicit contingency",
            "qualification": "Estimates only; no national build, physical-memory or source validation claim",
            "contingency": contingency, "host": host, "budgets": budgets, "results": results}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True, help="new report below data/")
    parser.add_argument("--construction-gib", type=float, default=8)
    parser.add_argument("--disk-reserve-gib", type=float, default=32)
    args = parser.parse_args()
    out = args.out.resolve()
    if not out.is_relative_to((Path.cwd() / "data").resolve()) or not out.parent.is_dir():
        parser.error("output must have an existing parent below this project's data/")
    if platform.system() == "Darwin":
        ram = int(subprocess.check_output(["sysctl", "-n", "hw.memsize"], text=True))
    else:
        ram = os.sysconf("SC_PHYS_PAGES") * os.sysconf("SC_PAGE_SIZE")
    host = {"ram_bytes": ram, "free_disk_bytes": shutil.disk_usage(out.parent).free,
            "platform": platform.platform(), "cpus": os.cpu_count(),
            "observed_utc": datetime.datetime.now(datetime.timezone.utc).isoformat()}
    try:
        result = estimate(json.loads(args.model.read_text()), host,
                          {"construction_bytes": args.construction_gib * GIB,
                           "disk_reserve_bytes": args.disk_reserve_gib * GIB})
        # Exclusive creation preserves every previous report, including failures.
        with out.open("x") as f:
            json.dump(result, f, indent=2, allow_nan=False)
            f.write("\n")
    except (ValueError, KeyError, OSError) as error:
        parser.error(str(error))
    for row in result["results"]:
        print(row["source"]["name"], row["decision"], "; ".join(row["reasons"]))
    return 2 if any(row["decision"] == "blocked" for row in result["results"]) else 0


if __name__ == "__main__":
    raise SystemExit(main())
