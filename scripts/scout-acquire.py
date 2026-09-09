#!/usr/bin/env python3
"""Pin and incrementally acquire Scout packages. No graph routing or deployment.

The complete provider digest is authoritative for package enumeration. Regional
lists only select initial inputs; they do not certify graph dependency closure.
"""
import argparse
import bz2
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import urllib.request

BASE = 'https://data.modrana.org/osm_scout_server/'
PREFIX = 'valhalla-34/valhalla/packages/'
MIB = 1 << 20
GIB = 1 << 30


def digest(data, algorithm='sha256'):
    return hashlib.new(algorithm, data).hexdigest()


def read_url(path, limit):
    with urllib.request.urlopen(BASE + path, timeout=60) as response:
        data = response.read(limit + 1)
    if len(data) > limit:
        raise ValueError('remote metadata exceeds budget: ' + path)
    return data


def write_new(path, value):
    with path.open('x') as out:
        json.dump(value, out, indent=2, sort_keys=True)
        out.write('\n')
        out.flush()
        os.fsync(out.fileno())


def read_local(path, limit=16 * MIB):
    with path.open('rb') as source:
        data = source.read(limit + 1)
    if len(data) > limit:
        raise ValueError('local metadata exceeds budget: ' + str(path))
    return data


def manifests(root):
    raw = read_local(root / 'digest.md5.bz2')
    decoder = bz2.BZ2Decompressor()
    decoded = decoder.decompress(raw, 32 * MIB + 1)
    if len(decoded) > 32 * MIB or not decoder.eof or decoder.unused_data:
        raise ValueError('invalid or oversized provider digest')
    entries = {}
    for line in decoded.decode().splitlines():
        md5, stamp, path = line.split()
        if not re.fullmatch('[0-9a-f]{32}', md5) or (path in entries and entries[path] != md5):
            raise ValueError('invalid/duplicate provider digest entry')
        entries[path] = md5
    packages = {p[len(PREFIX):-8] for p in entries
                if re.fullmatch(re.escape(PREFIX) + r'[0-9]+\.tar\.bz2', p)}
    listing = read_local(root / 'packages.html').decode()
    listed = re.findall(r'href="([0-9]+)\.tar\.bz2"', listing)
    if len(listed) != len(set(listed)) or packages != set(listed):
        raise ValueError('directory/full digest package inventory mismatch')
    catalog_bytes = read_local(root / 'catalog.json')
    if digest(catalog_bytes, 'md5') != entries['countries_provided.json']:
        raise ValueError('catalog/full digest mismatch')
    return entries, packages, json.loads(catalog_bytes)


def snapshot(args):
    args.root.mkdir()  # New metadata generation; never overwrite pins.
    for name, remote in [('catalog.json', 'countries_provided.json'),
                         ('digest.md5.bz2', 'digest.md5.bz2'),
                         ('packages.html', PREFIX)]:
        data = read_url(remote, 16 * MIB)
        with (args.root / name).open('xb') as out:
            out.write(data)
    manifests(args.root)
    print('metadata captured and internally consistent; source cutoff unverified')


def plan(args):
    root = args.root
    entries, packages, catalog = manifests(root)
    selected = set(args.extra.split(',')) if args.extra else set()
    regions = args.regions.split(',')
    matched = {k: v['valhalla'] for k, v in catalog.items() if isinstance(v, dict) and isinstance(v.get('valhalla'), dict)
               and any(k == r or k.startswith(r + '/') for r in regions)}
    if not matched:
        raise ValueError('no matching regional selections')
    generations = {(v['timestamp'], v['version']) for v in matched.values()}
    if len(generations) != 1 or next(iter(generations))[1] != '2':
        raise ValueError('mixed or unsupported catalog generation')
    for v in matched.values():
        selected.update(v['packages'])
    if not selected <= packages:
        raise ValueError('selection absent from complete provider manifest')
    union = {p for v in catalog.values() if isinstance(v, dict) and isinstance(v.get('valhalla'), dict) for p in v['valhalla']['packages']}

    def size_pin(pid):
        path = PREFIX + pid + '.tar.size-compressed'
        raw = read_url(path, 4096)
        if digest(raw, 'md5') != entries[path]:
            raise ValueError('size sidecar digest mismatch: ' + pid)
        size, name = raw.decode().split()
        if name != 'valhalla/packages/' + pid + '.tar.bz2':
            raise ValueError('size sidecar names wrong package')
        size = int(size)
        if not 0 < size <= 256 * MIB:
            raise ValueError('compressed package exceeds per-package budget')
        return dict(id=pid, bytes=size, md5=entries[PREFIX + pid + '.tar.bz2'],
                    url=BASE + PREFIX + pid + '.tar.bz2')

    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as workers:
        pins = list(workers.map(size_pin, sorted(selected, key=int)))
    total = sum(p['bytes'] for p in pins)
    free = shutil.disk_usage(root).free
    if total > args.download_gib * GIB or free - total < args.reserve_gib * GIB:
        raise ValueError(f'acquisition budget rejected: {total} bytes, {free} free')
    result = dict(schema=1, package_schema='2', tile_version='3.4.0',
                  timestamp=next(iter(generations))[0], packages=pins,
                  metadata_sha256={name: digest(read_local(root / name)) for name in
                                   ['catalog.json', 'digest.md5.bz2', 'packages.html']},
                  full_manifest_packages=len(packages),
                  catalog_omitted_packages=sorted(packages - union, key=int),
                  regions=sorted(matched), compressed_bytes=total,
                  budgets=dict(download_bytes=args.download_gib * GIB,
                               disk_reserve_bytes=args.reserve_gib * GIB),
                  provenance='Provider-byte consistency only; exact OSM cutoff and production build inputs are unverified.',
                  attribution=dict(text='© OpenStreetMap contributors',
                                   url='https://www.openstreetmap.org/copyright'))
    write_new(root / args.plan, result)
    print(json.dumps(dict(packages=len(pins), compressed_bytes=total,
                          free_bytes=free, plan=str(root / args.plan))), flush=True)


def file_hashes(path):
    md5, sha = hashlib.md5(), hashlib.sha256()
    with path.open('rb') as source:
        while data := source.read(MIB):
            md5.update(data)
            sha.update(data)
    return md5.hexdigest(), sha.hexdigest()


def fetch(args):
    root = args.root
    plan = json.loads(read_local(root / args.plan, 8 * MIB))
    if set(plan['metadata_sha256']) != {'catalog.json', 'digest.md5.bz2', 'packages.html'}:
        raise ValueError('complete provider metadata pins required')
    if (not 1 <= len(plan['packages']) <= 4096 or
            not 1 <= plan['budgets']['download_bytes'] <= args.download_gib * GIB or
            plan['budgets']['disk_reserve_bytes'] < args.reserve_gib * GIB):
        raise ValueError('invalid pinned acquisition budgets')
    for p in plan['packages']:
        if (not re.fullmatch('[0-9]+', p['id']) or not 0 < p['bytes'] <= 256 * MIB or
                not re.fullmatch('[0-9a-f]{32}', p['md5']) or
                (p.get('sha256') and not re.fullmatch('[0-9a-f]{64}', p['sha256'])) or
                p['url'] != BASE + PREFIX + p['id'] + '.tar.bz2'):
            raise ValueError('invalid provider package pin')
    for name, sha in plan['metadata_sha256'].items():
        if digest(read_local(root / name)) != sha:
            raise ValueError('local manifest changed: ' + name)
    # Recheck the catalog and *complete* provider digest before and after a run.
    # A changed unrelated entry conservatively rejects the attempted generation.
    def recheck():
        for name, remote in [('catalog.json', 'countries_provided.json'),
                             ('digest.md5.bz2', 'digest.md5.bz2')]:
            if digest(read_url(remote, 16 * MIB)) != plan['metadata_sha256'][name]:
                raise ValueError('provider generation changed: ' + remote)
    recheck()
    directory = root / 'packages'
    directory.mkdir(exist_ok=True)
    receipts = root / 'receipts'
    receipts.mkdir(exist_ok=True)
    if sum(p['bytes'] for p in plan['packages']) > plan['budgets']['download_bytes']:
        raise ValueError('plan exceeds its download budget')
    for p in plan['packages']:
        target = directory / (p['id'] + '.tar.bz2')
        receipt = receipts / (p['id'] + '.json')
        if not target.exists():
            # A failed transfer stays explicitly partial; retry discards only
            # this tool's partial file and never touches verified input bytes.
            partial = target.with_suffix('.partial')
            if shutil.disk_usage(root).free - p['bytes'] < plan['budgets']['disk_reserve_bytes']:
                raise ValueError('disk reserve would be crossed before package ' + p['id'])
            partial.unlink(missing_ok=True)
            md5, sha, total = hashlib.md5(), hashlib.sha256(), 0
            with urllib.request.urlopen(p['url'], timeout=60) as response, partial.open('xb') as out:
                if int(response.headers['Content-Length']) != p['bytes']:
                    raise ValueError('remote package size changed')
                while data := response.read(min(MIB, p['bytes'] - total + 1)):
                    total += len(data)
                    if total > p['bytes'] or shutil.disk_usage(root).free - len(data) < plan['budgets']['disk_reserve_bytes']:
                        raise ValueError('stream/disk budget exceeded')
                    md5.update(data)
                    sha.update(data)
                    out.write(data)
                out.flush()
                os.fsync(out.fileno())
            if (total != p['bytes'] or md5.hexdigest() != p['md5'] or
                    (p.get('sha256') and sha.hexdigest() != p['sha256'])):
                raise ValueError('package digest/length mismatch: ' + p['id'])
            # Atomic, refusing to replace an independently published file.
            os.link(partial, target)
            partial.unlink()
            hashes = md5.hexdigest(), sha.hexdigest()
        else:
            if target.stat().st_size != p['bytes']:
                raise ValueError('retained package size mismatch')
            hashes = file_hashes(target)
            if hashes[0] != p['md5'] or (p.get('sha256') and hashes[1] != p['sha256']):
                raise ValueError('retained package checksum mismatch')
        value = dict(p, sha256=hashes[1], generation=plan['metadata_sha256'])
        if receipt.exists():
            if json.loads(read_local(receipt, 65536)) != value:
                raise ValueError('existing receipt conflicts with pinned generation')
        else:
            write_new(receipt, value)
        print('verified', p['id'], p['bytes'], hashes[1], flush=True)
    recheck()
    print('complete; package bytes verified, graph dependency closure not yet certified', flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['snapshot', 'plan', 'fetch'])
    parser.add_argument('--root', type=Path, required=True)
    parser.add_argument('--plan', default='acquisition.json')
    parser.add_argument('--regions', default='north-america/us,north-america/canada,north-america/mexico')
    parser.add_argument('--extra', default='')
    parser.add_argument('--download-gib', type=int, default=12)
    parser.add_argument('--reserve-gib', type=int, default=32)
    args = parser.parse_args()
    if args.download_gib < 1 or args.reserve_gib < 32 or Path(args.plan).name != args.plan:
        parser.error('invalid budget or plan filename')
    {'snapshot': snapshot, 'plan': plan, 'fetch': fetch}[args.action](args)


if __name__ == '__main__':
    main()
