// Package qualification checks frozen Scout evidence independently of route search.
package qualification

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

const StateZIPHash = "9cbfe171dad1555e11770c981d8f4db9e687a65c86f5bdae684eeb487e2e9b80"

var States = strings.Fields("AL AK AZ AR CA CO CT DE DC FL GA HI ID IL IN IA KS KY LA ME MD MA MI MN MS MO MT NE NV NH NJ NM NY NC ND OH OK OR PA RI SC SD TN TX UT VT VA WA WV WI WY")

type Point [2]float64
type Box [4]float64
type Polygon struct {
	Box   Box
	Rings [][]Point
}

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, errors.New("verification input exceeds budget")
	}
	return b, nil
}
func readFile(path string, limit int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return readBounded(f, limit)
}
func Polygons(path string) (map[string]Polygon, error) {
	b, e := readFile(path, 16<<20)
	if e != nil {
		return nil, e
	}
	if fmt.Sprintf("%x", sha256.Sum256(b)) != StateZIPHash {
		return nil, errors.New("Census state input does not match pin")
	}
	z, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if e != nil {
		return nil, e
	}
	member := func(suffix string) ([]byte, error) {
		var found *zip.File
		for _, f := range z.File {
			if strings.HasSuffix(f.Name, suffix) {
				if found != nil {
					return nil, errors.New("duplicate Census member")
				}
				found = f
			}
		}
		if found == nil || found.UncompressedSize64 > 32<<20 {
			return nil, errors.New("missing/oversized Census member")
		}
		r, e := found.Open()
		if e != nil {
			return nil, e
		}
		defer r.Close()
		return readBounded(r, 32<<20)
	}
	shp, e := member(".shp")
	if e != nil {
		return nil, e
	}
	dbf, e := member(".dbf")
	if e != nil {
		return nil, e
	}
	return parsePolygons(shp, dbf)
}
func parsePolygons(shp, dbf []byte) (map[string]Polygon, error) {
	bad := errors.New("invalid Census Polygon/DBF extent")
	if len(dbf) < 33 || len(shp) < 100 {
		return nil, bad
	}
	le, be := binary.LittleEndian, binary.BigEndian
	records := int(le.Uint32(dbf[4:]))
	header, stride := int(le.Uint16(dbf[8:])), int(le.Uint16(dbf[10:]))
	if records < 1 || records > 1000 || header < 33 || header > len(dbf) || stride < 1 || records > (len(dbf)-header)/stride {
		return nil, bad
	}
	start, length, position := 0, 0, 1
	for at := 32; at+32 <= header-1; at += 32 {
		name := strings.TrimRight(string(dbf[at:at+11]), "\x00")
		size := int(dbf[at+16])
		if name == "STUSPS" {
			if length != 0 {
				return nil, bad
			}
			start, length = position, size
		}
		position += size
	}
	if length == 0 || start+length > stride || position > stride {
		return nil, bad
	}
	codes := make([]string, records)
	for i := range codes {
		at := header + i*stride
		if dbf[at] == '*' {
			return nil, bad
		}
		codes[i] = strings.TrimSpace(string(dbf[at+start : at+start+length]))
	}
	if be.Uint32(shp) != 9994 || le.Uint32(shp[32:]) != 5 || uint64(be.Uint32(shp[24:]))*2 != uint64(len(shp)) {
		return nil, bad
	}
	result := map[string]Polygon{}
	at, record := 100, 0
	for at < len(shp) {
		if at+8 > len(shp) || record >= records {
			return nil, bad
		}
		number, size := int(be.Uint32(shp[at:])), int(be.Uint32(shp[at+4:]))*2
		at += 8
		end := at + size
		if size < 44 || end > len(shp) || number != record+1 || le.Uint32(shp[at:]) != 5 {
			return nil, bad
		}
		var polygon Polygon
		for i := range polygon.Box {
			polygon.Box[i] = math.Float64frombits(le.Uint64(shp[at+4+8*i:]))
		}
		parts, points := int(le.Uint32(shp[at+36:])), int(le.Uint32(shp[at+40:]))
		if parts < 1 || points < 4 || parts > points || int64(44)+4*int64(parts)+16*int64(points) != int64(size) {
			return nil, bad
		}
		offsets := make([]int, parts+1)
		for i := 0; i < parts; i++ {
			offsets[i] = int(le.Uint32(shp[at+44+4*i:]))
		}
		offsets[parts] = points
		if offsets[0] != 0 {
			return nil, bad
		}
		for i := 0; i < parts; i++ {
			if offsets[i] < 0 || offsets[i+1]-offsets[i] < 4 || offsets[i+1] > points {
				return nil, bad
			}
			ring := make([]Point, offsets[i+1]-offsets[i])
			for j := range ring {
				off := at + 44 + 4*parts + 16*(offsets[i]+j)
				ring[j] = Point{math.Float64frombits(le.Uint64(shp[off:])), math.Float64frombits(le.Uint64(shp[off+8:]))}
				if !valid(ring[j]) {
					return nil, bad
				}
			}
			if ring[0] != ring[len(ring)-1] {
				return nil, bad
			}
			polygon.Rings = append(polygon.Rings, ring)
		}
		if _, exists := result[codes[record]]; exists {
			return nil, bad
		}
		result[codes[record]] = polygon
		record++
		at = end
	}
	if record != records {
		return nil, bad
	}
	selected := map[string]Polygon{}
	for _, state := range States {
		p, ok := result[state]
		if !ok {
			return nil, errors.New("incomplete state polygons")
		}
		selected[state] = p
	}
	return selected, nil
}
func valid(p Point) bool {
	return !math.IsNaN(p[0]) && !math.IsNaN(p[1]) && math.Abs(p[0]) <= 180 && math.Abs(p[1]) <= 90
}
func Contains(p Polygon, q Point) bool {
	x, y := q[0], q[1]
	if x < p.Box[0] || x > p.Box[2] || y < p.Box[1] || y > p.Box[3] {
		return false
	}
	inside := false
	for _, ring := range p.Rings {
		for i := 1; i < len(ring); i++ {
			a, b := ring[i-1], ring[i]
			if (a[1] > y) != (b[1] > y) && x < a[0]+(b[0]-a[0])*(y-a[1])/(b[1]-a[1]) {
				inside = !inside
			}
		}
	}
	return inside
}
func Intersects(p Polygon, b Box) bool {
	if p.Box[2] < b[0] || p.Box[0] > b[2] || p.Box[3] < b[1] || p.Box[1] > b[3] {
		return false
	}
	for _, q := range []Point{{b[0], b[1]}, {b[2], b[1]}, {b[2], b[3]}, {b[0], b[3]}} {
		if Contains(p, q) {
			return true
		}
	}
	for _, ring := range p.Rings {
		for i := 1; i < len(ring); i++ {
			a, c := ring[i-1], ring[i]
			lo, hi := 0.0, 1.0
			for axis := 0; axis < 2; axis++ {
				delta := c[axis] - a[axis]
				if delta == 0 {
					if a[axis] < b[axis] || a[axis] > b[axis+2] {
						lo, hi = 1, 0
						break
					}
				} else {
					near, far := (b[axis]-a[axis])/delta, (b[axis+2]-a[axis])/delta
					if near > far {
						near, far = far, near
					}
					lo, hi = math.Max(lo, near), math.Min(hi, far)
				}
			}
			if lo <= hi {
				return true
			}
		}
	}
	return false
}
func Coverage(boundaries, auditPath, routes string) (map[string]any, error) {
	states, e := Polygons(boundaries)
	if e != nil {
		return nil, e
	}
	raw, e := readFile(auditPath, 64<<20)
	if e != nil {
		return nil, e
	}
	var wrapper map[string]json.RawMessage
	if e = json.Unmarshal(raw, &wrapper); e != nil {
		return nil, e
	}
	if p, ok := wrapper["partial_audit"]; ok {
		raw = p
	}
	var audit struct {
		MissingReferences map[string]int
		GeometryExamples  []json.RawMessage
	}
	if e = json.Unmarshal(raw, &audit); e != nil {
		return nil, e
	}
	var required map[string]json.RawMessage
	json.Unmarshal(raw, &required)
	if required["MissingReferences"] == nil || required["GeometryExamples"] == nil {
		return nil, errors.New("incomplete audit report")
	}
	missing, badGeometry := []any{}, []any{}
	ids := make([]uint64, 0, len(audit.MissingReferences))
	for key := range audit.MissingReferences {
		id, e := strconv.ParseUint(key, 10, 64)
		if e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		level, tile := int(id&7), id>>3
		if level > 2 || tile >= uint64([3]int{4050, 64800, 1036800}[level]) {
			return nil, errors.New("invalid missing tile ID")
		}
		step, width := [3]float64{4, 1, .25}[level], uint64([3]int{90, 360, 1440}[level])
		x, y := float64(tile%width)*step-180, float64(tile/width)*step-90
		box := Box{x, y, x + step, y + step}
		touching := []string{}
		for _, code := range States {
			if Intersects(states[code], box) {
				touching = append(touching, code)
			}
		}
		if len(touching) > 0 {
			missing = append(missing, map[string]any{"tile": id, "states": touching, "box": box})
		}
	}
	for _, rawIssue := range audit.GeometryExamples {
		var issue struct{ Start, End, ShapeStart, ShapeEnd Point }
		if e := json.Unmarshal(rawIssue, &issue); e != nil {
			return nil, e
		}
		touching := []string{}
		for _, code := range States {
			for _, point := range []Point{issue.Start, issue.End, issue.ShapeStart, issue.ShapeEnd} {
				if Contains(states[code], point) {
					touching = append(touching, code)
					break
				}
			}
		}
		if len(touching) > 0 {
			badGeometry = append(badGeometry, map[string]any{"issue": rawIssue, "states": touching})
		}
	}
	sortedStates := append([]string{}, States...)
	sort.Strings(sortedStates)
	result := map[string]any{"boundary_sha256": StateZIPHash, "states": sortedStates, "missing_reference_tiles_touching_states": missing, "geometry_examples_inside_states": badGeometry, "limitation": "Simplified 1:500,000 Census polygons; retained dependencies against coarse state footprints, not source road completeness or surveyed boundaries."}
	if routes != "" {
		covered := map[string]bool{}
		failures := []any{}
		count := 0
		e = EachRow(routes, func(raw []byte) error {
			count++
			if count > 501 {
				return errors.New("too many route rows")
			}
			var row struct {
				Case     struct{ Name, State string }
				Outcome  string
				Verified bool
				Route    struct{ Origin, Destination struct{ Point Point } }
			}
			if e := json.Unmarshal(raw, &row); e != nil {
				return e
			}
			polygon, ok := states[row.Case.State]
			if !ok {
				return nil
			}
			if row.Outcome != "routed" || !row.Verified || !Contains(polygon, row.Route.Origin.Point) || !Contains(polygon, row.Route.Destination.Point) {
				failures = append(failures, row.Case)
			} else {
				covered[row.Case.State] = true
			}
			return nil
		})
		if e != nil {
			return nil, e
		}
		yes, no := []string{}, []string{}
		for _, code := range sortedStates {
			if covered[code] {
				yes = append(yes, code)
			} else {
				no = append(no, code)
			}
		}
		result["route_states"], result["missing_route_states"], result["route_failures"] = yes, no, failures
	}
	return result, nil
}
