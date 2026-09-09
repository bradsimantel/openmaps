#!/usr/bin/env python3
"""Independent coarse state checks using pinned Census cartographic polygons.

This reads the fixed Polygon/DBF formats needed by the Census state file, not a
general GIS importer. It does not create a legal boundary mask or certify OSM
road completeness. No network access occurs in this verification command.
"""
import argparse
import hashlib
import json
from pathlib import Path
import struct
import zipfile

STATE_ZIP_SHA256 = '9cbfe171dad1555e11770c981d8f4db9e687a65c86f5bdae684eeb487e2e9b80'
STATES = set('AL AK AZ AR CA CO CT DE DC FL GA HI ID IL IN IA KS KY LA ME MD MA MI MN MS MO MT NE NV NH NJ NM NY NC ND OH OK OR PA RI SC SD TN TX UT VT VA WA WV WI WY'.split())


def polygons(path):
    with path.open('rb') as source:
        raw = source.read((16 << 20) + 1)
    if len(raw) > 16 << 20 or hashlib.sha256(raw).hexdigest() != STATE_ZIP_SHA256:
        raise ValueError('Census state input does not match pin')
    with zipfile.ZipFile(path) as z:
        def member(suffix):
            info, = [i for i in z.infolist() if i.filename.endswith(suffix)]
            if info.file_size > 32 << 20:
                raise ValueError('Census member exceeds budget')
            return z.read(info)
        shp, dbf = member('.shp'), member('.dbf')
    records = struct.unpack_from('<I', dbf, 4)[0]
    header, stride = struct.unpack_from('<HH', dbf, 8)
    fields, position = {}, 1
    for at in range(32, header - 1, 32):
        name = dbf[at:at+11].split(b'\0')[0].decode()
        size = dbf[at+16]
        fields[name] = (position, size)
        position += size
    start, length = fields['STUSPS']
    codes = [dbf[header+i*stride+start:header+i*stride+start+length].decode().strip() for i in range(records)]
    if struct.unpack_from('>I', shp, 0)[0] != 9994 or struct.unpack_from('<I', shp, 32)[0] != 5:
        raise ValueError('expected Census Polygon shapefile')
    result, at, record = {}, 100, 0
    while at < len(shp):
        number, size = struct.unpack_from('>II', shp, at)
        at += 8
        end = at+size*2
        if end > len(shp) or number != record+1 or struct.unpack_from('<I', shp, at)[0] != 5:
            raise ValueError('invalid polygon record')
        box = struct.unpack_from('<4d', shp, at+4)
        parts, points = struct.unpack_from('<II', shp, at+36)
        if at+44+4*parts+16*points != end:
            raise ValueError('unexpected polygon extent')
        offsets = list(struct.unpack_from('<'+str(parts)+'I', shp, at+44))+[points]
        vertices = list(struct.iter_unpack('<dd', shp[at+44+4*parts:end]))
        rings = [vertices[offsets[i]:offsets[i+1]] for i in range(parts)]
        result[codes[record]] = (box, rings)
        record, at = record+1, end
    if record != records or not STATES <= result.keys():
        raise ValueError('incomplete state polygons')
    return {k:v for k,v in result.items() if k in STATES}


def contains(polygon, point):
    box, rings = polygon
    x, y = point
    if not box[0] <= x <= box[2] or not box[1] <= y <= box[3]:
        return False
    inside = False
    for ring in rings:
        for a, b in zip(ring, ring[1:]):
            if (a[1] > y) != (b[1] > y) and x < a[0]+(b[0]-a[0])*(y-a[1])/(b[1]-a[1]):
                inside = not inside
    return inside


def intersects(polygon, box):
    pbox, rings = polygon
    if pbox[2] < box[0] or pbox[0] > box[2] or pbox[3] < box[1] or pbox[1] > box[3]:
        return False
    corners = [(box[0],box[1]),(box[2],box[1]),(box[2],box[3]),(box[0],box[3])]
    if any(contains(polygon,p) for p in corners):
        return True
    for ring in rings:
        for a,b in zip(ring,ring[1:]):
            if box[0] <= a[0] <= box[2] and box[1] <= a[1] <= box[3]:
                return True
            # Clip the source segment to the tile rectangle (Liang–Barsky).
            lo, hi = 0.0, 1.0
            for axis in [0,1]:
                delta=b[axis]-a[axis]
                if delta == 0:
                    if not box[axis] <= a[axis] <= box[axis+2]:
                        lo,hi=1,0
                        break
                else:
                    near,far=sorted(((box[axis]-a[axis])/delta,(box[axis+2]-a[axis])/delta))
                    lo,hi=max(lo,near),min(hi,far)
            if lo<=hi:
                return True
    return False


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--boundaries',type=Path,required=True)
    p.add_argument('--audit',type=Path,required=True)
    p.add_argument('--routes',type=Path)
    a=p.parse_args()
    states=polygons(a.boundaries)
    with a.audit.open('rb') as source:
        raw=source.read((64 << 20)+1)
    if len(raw)>64 << 20:
        raise ValueError('audit exceeds metadata budget')
    audit=json.loads(raw)
    audit=audit.get('partial_audit',audit)
    missing=[]
    for value in audit['MissingReferences']:
        value=int(value);level=value&7;tile=value>>3
        step=[4,1,.25][level];width=[90,360,1440][level]
        x=(tile%width)*step-180;y=(tile//width)*step-90
        box=(x,y,x+step,y+step)
        touching=[code for code,polygon in states.items() if intersects(polygon,box)]
        if touching:missing.append(dict(tile=value,states=touching,box=box))
    bad_geometry=[]
    for issue in audit['GeometryExamples']:
        touching=[code for code,polygon in states.items() if any(contains(polygon,issue[k]) for k in ['Start','End','ShapeStart','ShapeEnd'])]
        if touching:bad_geometry.append(dict(issue=issue,states=touching))
    result=dict(boundary_sha256=STATE_ZIP_SHA256,states=sorted(states),missing_reference_tiles_touching_states=missing,geometry_examples_inside_states=bad_geometry,
                limitation='Simplified 1:500,000 Census polygons; verifies retained dependencies against coarse state footprints, not source road completeness or surveyed boundaries.')
    if a.routes:
        covered=set();failures=[]
        with a.routes.open() as source:
            lines = []
            while line := source.readline(64 << 20):
                if len(line) >= 64 << 20 or len(lines) > 501:
                    raise ValueError('route report exceeds verification budget')
                # Discard geometry immediately; only endpoint/status evidence is retained.
                row = json.loads(line)
                route = row.get('route', {})
                row['route'] = {k: route[k] for k in ['Origin', 'Destination'] if k in route}
                lines.append(row)
        for row in lines:
            code=row.get('case',{}).get('state')
            if code not in STATES:continue
            route=row.get('route',{})
            if row.get('outcome') != 'routed' or not row.get('verified') or not all(contains(states[code],route[k]['Point']) for k in ['Origin','Destination']):
                failures.append(row.get('case'))
            else:covered.add(code)
        result.update(route_states=sorted(covered),missing_route_states=sorted(STATES-covered),route_failures=failures)
    print(json.dumps(result,indent=2))


if __name__=='__main__':main()
