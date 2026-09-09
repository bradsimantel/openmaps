#!/usr/bin/env python3
"""Run ONE retained workload with read-only macOS resource sampling.

Usage: python3 scripts/routing-residency.py NEW_OUTPUT_DIR COMMAND [ARG ...]
Uses only Python's standard library and macOS proc_pid_rusage v2 (SDK
sys/resource.h). No cache purge, pressure generation or process limit is applied.
The command inherits explicit harness environment variables. Samples are not
peaks; time -l may be used separately for lifetime maxima. All outputs must be
under ignored data/ and the output directory must not already exist.
"""
import ctypes
import json
import os
from pathlib import Path
import subprocess
import sys
import time


def main():
    if sys.platform != "darwin" or len(sys.argv) < 3:
        sys.exit("requires macOS: NEW_OUTPUT_DIR COMMAND [ARG ...]")
    out = Path(sys.argv[1]).resolve()
    if not out.is_relative_to(Path("data").resolve()):
        sys.exit("output must be under repository data/")
    out.mkdir()
    names = "user_time system_time pkg_idle_wkups interrupt_wkups pageins wired_size resident_size phys_footprint proc_start_abstime proc_exit_abstime child_user_time child_system_time child_pkg_idle_wkups child_interrupt_wkups child_pageins child_elapsed_abstime diskio_bytesread diskio_byteswritten".split()

    class Usage(ctypes.Structure):
        _fields_ = [("uuid", ctypes.c_uint8 * 16)] + [(n, ctypes.c_uint64) for n in names]

    lib = ctypes.CDLL("/usr/lib/libproc.dylib", use_errno=True)
    lib.proc_pid_rusage.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.c_void_p]
    lib.proc_pid_rusage.restype = ctypes.c_int
    metadata = {"command": sys.argv[2:], "environment": {k: v for k, v in os.environ.items() if k.startswith("OPENMAPS_") or k in ("GOMEMLIMIT", "GOGC")}, "sample_seconds": 0.1, "controls": "none; previously accessed files; GOMEMLIMIT is not RSS", "started_unix": time.time()}
    (out / "run.json").write_text(json.dumps(metadata, indent=2))
    def host(stage):
        with (out / (stage+"-host.txt")).open("w") as f:
            for cmd in (["vm_stat"], ["sysctl", "hw.memsize", "vm.swapusage"], ["memory_pressure", "-Q"]):
                subprocess.run(cmd, stdout=f, stderr=f, timeout=10)
    host("before")
    with (out / "workload.txt").open("w") as log, (out / "samples.jsonl").open("w") as samples:
        start = time.monotonic()
        p = subprocess.Popen(sys.argv[2:], stdout=log, stderr=subprocess.STDOUT)
        next_maps = start
        map_index = 0
        while p.poll() is None:
            u = Usage()
            if lib.proc_pid_rusage(p.pid, 2, ctypes.byref(u)) == 0:
                record = {n: getattr(u, n) for n in names}
                record["elapsed_s"] = time.monotonic()-start
                samples.write(json.dumps(record)+"\n")
                samples.flush()
            if os.environ.get("OPENMAPS_RESIDENCY_MAPS") == "1" and time.monotonic() >= next_maps:
                for tool, args in (("vmmap", ["-wide"]), ("footprint", ["-w", "-f", "bytes", "-p"])):
                    with (out / (str(map_index)+"-"+tool+".txt")).open("w") as f:
                        try:
                            subprocess.run([tool, *args, str(p.pid)], stdout=f, stderr=f, timeout=10)
                        except subprocess.TimeoutExpired:
                            f.write("sampling timed out\n")
                map_index += 1
                next_maps = time.monotonic() + 2
            time.sleep(0.1)
        code = p.returncode
    host("after")
    (out / "exit.json").write_text(json.dumps({"code": code, "seconds": time.monotonic()-start}))
    return code


if __name__ == "__main__":
    sys.exit(main())
