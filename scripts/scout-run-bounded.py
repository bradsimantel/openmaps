#!/usr/bin/env python3
"""Observe one candidate job, terminate it on RSS/disk budget crossing.

Sampling is not a hard RSS limit and cannot observe every transient peak.
No host settings, unrelated processes or files are changed.
"""
import argparse
import json
import os
from pathlib import Path
import resource
import shutil
import signal
import subprocess
import time


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--root', type=Path, required=True)
    p.add_argument('--report', type=Path, required=True)
    p.add_argument('--rss-mib', type=int, default=4096)
    p.add_argument('--reserve-gib', type=int, default=32)
    p.add_argument('command', nargs=argparse.REMAINDER)
    a = p.parse_args()
    if not a.command or a.rss_mib < 1 or a.rss_mib > 8192 or a.reserve_gib < 32:
        p.error('command and valid headroom budgets required')
    report = a.report.open('x')
    before = shutil.disk_usage(a.root).free
    if before < a.reserve_gib * (1 << 30):
        raise RuntimeError('disk reserve already crossed')
    start = time.monotonic()
    child = subprocess.Popen(a.command, start_new_session=True)
    peak = 0
    reason = None
    try:
        while child.poll() is None:
            result = subprocess.run(['ps', '-o', 'rss=', '-p', str(child.pid)],
                                    capture_output=True, text=True, check=False)
            if result.stdout.strip():
                rss = int(result.stdout.strip()) * 1024
                peak = max(peak, rss)
                if rss > a.rss_mib * (1 << 20):
                    reason = 'sampled RSS exceeded budget'
            if shutil.disk_usage(a.root).free < a.reserve_gib * (1 << 30):
                reason = 'disk reserve crossed'
            if reason:
                os.killpg(child.pid, signal.SIGTERM)
                try:
                    child.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    os.killpg(child.pid, signal.SIGKILL)
                    child.wait()
                break
            time.sleep(.2)
    except BaseException:
        if child.poll() is None:
            os.killpg(child.pid, signal.SIGTERM)
            try:
                child.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(child.pid, signal.SIGKILL)
                child.wait()
        raise
    finally:
        usage = resource.getrusage(resource.RUSAGE_CHILDREN)
        json.dump(dict(command=a.command, seconds=time.monotonic() - start,
                       sampled_peak_rss_bytes=peak, abort_reason=reason,
                       returncode=child.poll(), free_before=before,
                       free_after=shutil.disk_usage(a.root).free,
                       budgets=dict(rss_mib=a.rss_mib, reserve_gib=a.reserve_gib),
                       child_maxrss_native_units=usage.ru_maxrss,
                       note='Sampled child RSS; not a hard RSS or total OS-cache bound'), report, indent=2)
        report.write('\n')
        report.close()
    raise SystemExit(child.returncode or (2 if reason else 0))


if __name__ == '__main__':
    main()
