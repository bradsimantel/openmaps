#!/usr/bin/env python3
"""Measure full responses against previously source-verified offline results.

No downloads or deployment mutations. Each response body is bounded; the first
response per case is retained. Concurrent loops exercise the selected service.
"""
import argparse
import concurrent.futures
import hashlib
import json
import math
from pathlib import Path
import threading
import time
import urllib.error
import urllib.request

LIMIT = 64 << 20
MASK = 'routes.distanceMeters,routes.duration,routes.staticDuration,routes.polyline'


def bounded_lines(path):
    with path.open('rb') as source:
        while line := source.readline(LIMIT+1):
            if len(line)>LIMIT:
                raise ValueError('offline row exceeds budget')
            yield json.loads(line)


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--url', default='http://127.0.0.1:8097')
    p.add_argument('--offline', type=Path, action='append', required=True)
    p.add_argument('--out', type=Path, required=True)
    p.add_argument('--workers', type=int, default=1)
    p.add_argument('--seconds', type=int, default=0)
    a=p.parse_args()
    if not 1<=a.workers<=4 or not 0<=a.seconds<=1800:
        p.error('workers 1..4, seconds 0..1800')
    cases={}
    for path in a.offline:
        for row in bounded_lines(path):
            if 'case' in row and row['outcome']==(row['case'].get('expect') or 'routed') and (row.get('verified') or row['outcome'] in ['unreachable','unsnappable']):
                cases[row['case']['name']]=row
    cases=list(cases.values())
    if not 1<=len(cases)<=500:
        raise ValueError('require 1..500 verified offline cases')
    a.out.mkdir()
    lock=threading.Lock()
    seen=set()
    results=[]
    started=time.monotonic()
    output=(a.out/'requests.jsonl').open('x')
    def request(index, worker):
        case=cases[index%len(cases)];c=case['case']
        def point(v):return {'location':{'latLng':{'longitude':v[0],'latitude':v[1]}}}
        body={'origin':point(c['from']),'destination':point(c['to']),'polylineEncoding':'GEO_JSON_LINESTRING'}
        req=urllib.request.Request(a.url+'/directions/v2:computeRoutes',data=json.dumps(body).encode(),headers={'Content-Type':'application/json','X-Goog-FieldMask':MASK})
        begin=time.monotonic();result=dict(case=c['name'],worker=worker,index=index,started_seconds=begin-started)
        raw=b''
        try:
            try:response=urllib.request.urlopen(req,timeout=40)
            except urllib.error.HTTPError as e:response=e
            with response:
                raw=response.read(LIMIT+1);result['status']=response.status
            if len(raw)>LIMIT:raise ValueError('response exceeds budget')
            got=json.loads(raw);meta=got.get('openmaps',{})
            result.update(bytes=len(raw),sha256=hashlib.sha256(raw).hexdigest(),outcome=meta.get('outcome'),snapshot=meta.get('snapshot'))
            want_status=400 if case['outcome']=='unsnappable' else 200
            if result['status']!=want_status or meta.get('outcome')!=case['outcome']:
                raise ValueError('status/outcome differs from verified offline case')
            if meta.get('profile')!='osm-scout-public-auto-v1' or not meta.get('snapshot') or not meta.get('attribution'):
                raise ValueError('missing profile/source identity')
            if case['outcome']=='routed':
                r=got['routes'][0];want=case['route']
                if r['distanceMeters']!=math.floor(want['Meters']+.5) or r['duration']!=str(math.floor(want['Seconds']+.5))+'s' or r['staticDuration']!=r['duration']:
                    raise ValueError('cost differs from source-verified path')
                if r['polyline']['geoJsonLinestring']!={'type':'LineString','coordinates':want['Geometry']}:
                    raise ValueError('full geometry differs from source-verified path')
            result['passed']=True
        except Exception as e:
            result.update(passed=False,error=str(e))
        result['seconds']=time.monotonic()-begin
        with lock:
            if c['name'] not in seen:
                (a.out/f'response-{index%len(cases):03d}.json').write_bytes(raw);seen.add(c['name'])
            output.write(json.dumps(result)+'\n');output.flush();results.append(result)
        return result['passed']
    def run(worker):
        index=worker
        while True:
            request(index,worker)
            index+=a.workers
            if a.seconds==0:
                if index>=len(cases):break
            elif time.monotonic()-started>=a.seconds:break
    with concurrent.futures.ThreadPoolExecutor(max_workers=a.workers) as pool:
        list(pool.map(run,range(a.workers if a.seconds else min(a.workers,len(cases)))))
    output.close()
    times=sorted(x['seconds'] for x in results)
    def percentile(q):return times[min(len(times)-1,math.ceil(q*len(times))-1)]
    summary=dict(seconds=time.monotonic()-started,workers=a.workers,cases=len(cases),requests=len(results),passed=sum(x['passed'] for x in results),failures=[x for x in results if not x['passed']],bytes=sum(x.get('bytes',0) for x in results),p50_seconds=percentile(.5),p95_seconds=percentile(.95),max_seconds=max(times),snapshots=sorted({x.get('snapshot','') for x in results}),note='Full JSON receive/parse and geometry equality; shared host OS cache, no physical-cold claim')
    (a.out/'summary.json').write_text(json.dumps(summary,indent=2)+'\n')
    print(json.dumps(summary))
    raise SystemExit(1 if summary['failures'] else 0)


if __name__=='__main__':main()
