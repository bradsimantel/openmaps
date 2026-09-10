package importer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"
)

func testFeature(id string, properties map[string]any) map[string]any {
	properties["id"] = id
	properties["sources"] = []any{map[string]any{"dataset": "synthetic", "record_id": id, "property": ""}}
	return map[string]any{"type": "Feature", "geometry": map[string]any{"type": "Point", "coordinates": []float64{-71.31, 41.49}}, "properties": properties}
}
func testSegment(id, subtype, name string, coordinates [][2]float64, aliases ...string) map[string]any {
	rules := []any{}
	for _, alias := range aliases {
		rules = append(rules, map[string]any{"value": alias, "variant": "alternate"})
	}
	common := [][]string{{"en", name}}
	if name == "Example Street" {
		common = append(common, []string{"es", "Calle Ejemplo"})
	}
	return map[string]any{
		"type":     "Feature",
		"geometry": map[string]any{"type": "LineString", "coordinates": coordinates},
		"properties": map[string]any{
			"id": id, "subtype": subtype,
			"names":   map[string]any{"primary": name, "common": common, "rules": rules},
			"sources": []any{map[string]any{"dataset": "synthetic-roads", "record_id": "road-" + id, "property": ""}},
		},
	}
}
func regionFixture(t *testing.T) (Manifest, string) {
	t.Helper()
	dir := t.TempDir()
	m := Manifest{Schema: 1, Region: "synthetic", BBox: [4]float64{-71.33, 41.47, -71.29, 41.51}, CoordinateOrder: "longitude,latitude"}
	fixtures := map[string][]any{
		"place":    {testFeature("bakery", map[string]any{"names": map[string]any{"primary": "Fixture Bakery", "common": [][]string{{"en", "Bread Shop"}, {"fr", "Boulangerie"}}}, "addresses": []any{map[string]any{"freeform": "26 Example St", "postcode": "02840"}}, "websites": []string{"https://example.org"}}), testFeature("phantom", map[string]any{"names": map[string]any{"primary": "Phantom Business"}, "addresses": []any{map[string]any{"freeform": "99 Phantom Street", "postcode": "02840"}}})},
		"address":  {testFeature("address-a", map[string]any{"number": "26", "street": "Example Street", "postcode": "02840", "country": "US"})},
		"division": {testFeature("town", map[string]any{"names": map[string]any{"primary": "Fixture Town"}, "subtype": "locality", "parent_division_id": "absent-country"})},
		"segment": {
			testSegment("road-a", "road", "Example Street", [][2]float64{{-71.34, 41.48}, {-71.30, 41.48}}, "Old Example Road"),
			testSegment("road-b", "road", "", [][2]float64{{-71.32, 41.49}, {-71.31, 41.49}}),
			testSegment("road-c", "road", "Example Street", [][2]float64{{-71.32, 41.50}, {-71.31, 41.50}}),
			testSegment("rail-a", "rail", "Fixture Railway", [][2]float64{{-71.32, 41.49}, {-71.31, 41.49}}),
			testSegment("outside", "road", "Outside Road", [][2]float64{{-71.40, 41.49}, {-71.39, 41.49}}),
		},
	}
	for _, kind := range []string{"place", "address", "division", "segment"} {
		name := kind + ".geojson"
		if e := WriteJSON(filepath.Join(dir, name), map[string]any{"type": "FeatureCollection", "features": fixtures[kind]}); e != nil {
			t.Fatal(e)
		}
		hash, _ := Checksum(filepath.Join(dir, name))
		m.Inputs = append(m.Inputs, Input{File: name, SHA256: hash, Release: "fixture-v1", URL: "s3://fixture/type=" + kind + "/", Attribution: "synthetic"})
	}
	return m, dir
}
func TestPrepareProviderRecordsAndAmbiguity(t *testing.T) {
	m, dir := regionFixture(t)
	b, a, e := Prepare(context.Background(), m, dir, map[string]string{})
	if e != nil {
		t.Fatal(e)
	}
	if len(b.Records) != 6 || len(b.Relationships) != 1 || b.Relationships[0].To != "overture:address:address-a" {
		t.Fatalf("unexpected import: %+v", b)
	}
	selection, ok := a["transportation_selection"].(transportationSelection)
	if !ok || selection != (transportationSelection{Segments: 5, NamedRoads: 2, UnnamedRoads: 1, NonRoads: 1, OutsideGeometry: 1}) {
		t.Fatalf("unexpected transportation selection: %#v", a["transportation_selection"])
	}
	var bakery, road Record
	for _, r := range b.Records {
		if r.SourceID == "bakery" {
			bakery = r
		}
		if r.SourceID == "road-a" {
			road = r
		}
	}
	if !bytes.Contains(bakery.Raw, []byte(`"record_id":"bakery"`)) || string(bakery.Attributes["aliases"]) != `["Boulangerie","Bread Shop"]` {
		t.Fatalf("lost upstream identity/aliases: %+v", bakery)
	}
	if road.Source != "overture:segment" || !bytes.Contains(road.Raw, []byte(`"record_id":"road-road-a"`)) || !bytes.Contains(road.Raw, []byte(`"coordinates":[[-71.34,41.48],[-71.3,41.48]]`)) || string(road.Attributes["aliases"]) != `["Calle Ejemplo","Old Example Road"]` || string(road.Attributes["location"]) != `{"lat":41.48,"lng":-71.315}` || road.Key() != "overture:segment:road-a" {
		t.Fatalf("lost transportation identity, provenance, or aliases: %+v", road)
	}
	if len(road.Attributions) != 1 || road.Attributions[0].Provider != "© OpenStreetMap contributors, Overture Maps Foundation" {
		t.Fatalf("lost transportation attribution: %+v", road.Attributions)
	}
	if PublicID(road.Key()) != "om_07531994ffec36df496da5a8b2555933" {
		t.Fatal("street public ID is not deterministic")
	}
	rebuilt, _, e := Prepare(context.Background(), m, dir, map[string]string{})
	if e != nil || !reflect.DeepEqual(b, rebuilt) {
		t.Fatal("nondeterministic preparation", e)
	}
	// Repeated labels are separate addresses, and ambiguity suppresses the link.
	address := testFeature("address-a", map[string]any{"number": "26", "street": "Example Street", "postcode": "02840"})
	second := testFeature("address-b", map[string]any{"number": "26", "street": "Example Street", "postcode": "02840"})
	path := filepath.Join(dir, "address.geojson")
	if e = WriteJSON(path, map[string]any{"type": "FeatureCollection", "features": []any{address, second}}); e != nil {
		t.Fatal(e)
	}
	if _, _, e = Prepare(context.Background(), m, dir, nil); e == nil || !strings.Contains(e.Error(), "checksum mismatch") {
		t.Fatal("accepted changed source", e)
	}
	m.Inputs[1].SHA256, _ = Checksum(path)
	b, a, e = Prepare(context.Background(), m, dir, nil)
	if e != nil || len(b.Relationships) != 0 || len(b.Records) != 7 || a["ambiguous_business_addresses"] != 1 {
		t.Fatal("ambiguity handling", a, e)
	}
}
func TestPreparationCancellation(t *testing.T) {
	m, dir := regionFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, e := Prepare(ctx, m, dir, nil); e == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestTransportationRepresentativeCoordinateBoundaries(t *testing.T) {
	bounds := [4]float64{-1, -1, 1, 1}
	for _, tc := range []struct {
		name   string
		points [][2]float64
		want   [2]float64
		ok     bool
	}{
		{"inside", [][2]float64{{-0.5, 0}, {0.5, 0}}, [2]float64{0, 0}, true},
		{"crossing", [][2]float64{{-2, 0}, {2, 0}}, [2]float64{0, 0}, true},
		{"boundary", [][2]float64{{-2, 1}, {0, 1}}, [2]float64{-0.5, 1}, true},
		{"outside", [][2]float64{{-2, 2}, {2, 2}}, [2]float64{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := representativeCoordinate(tc.points, bounds)
			if ok != tc.ok || math.Abs(got[0]-tc.want[0]) > 1e-9 || math.Abs(got[1]-tc.want[1]) > 1e-9 {
				t.Fatalf("got %v,%v want %v,%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}
func TestDistanceAndBounds(t *testing.T) {
	if math.Abs(distance([2]float64{179.999, 0}, [2]float64{-179.999, 0})-222.39016) > 0.01 {
		t.Fatal("dateline distance")
	}
	b := [4]float64{-71.33, 41.47, -71.29, 41.51}
	if !inside([2]float64{-71.33, 41.47}, b) || inside([2]float64{-71.330001, 41.47}, b) {
		t.Fatal("region edges")
	}
}
func TestParquetNamesAndCoordinates(t *testing.T) {
	type names struct {
		Primary string             `parquet:"primary"`
		Common  map[string]*string `parquet:"common"`
	}
	type row struct {
		ID         string  `parquet:"id"`
		Geometry   []byte  `parquet:"geometry"`
		Names      names   `parquet:"names"`
		Confidence float32 `parquet:"confidence"`
	}
	wkb := make([]byte, 21)
	wkb[0] = 1
	binary.LittleEndian.PutUint32(wkb[1:], 1)
	binary.LittleEndian.PutUint64(wkb[5:], math.Float64bits(-71.31))
	binary.LittleEndian.PutUint64(wkb[13:], math.Float64bits(41.49))
	var buf bytes.Buffer
	writer := parquet.NewGenericWriter[row](&buf)
	if _, e := writer.Write([]row{{ID: "gers-fixture", Geometry: wkb, Names: names{Primary: "Newport", Common: map[string]*string{"ko": new("뉴포트"), "en": new("Newport")}}, Confidence: 0.9}}); e != nil {
		t.Fatal(e)
	}
	if e := writer.Close(); e != nil {
		t.Fatal(e)
	}
	pf, e := parquet.OpenFile(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if e != nil {
		t.Fatal(e)
	}
	r := parquet.NewGenericReader[any](pf)
	defer r.Close()
	rows := make([]any, 1)
	if _, e = r.Read(rows); e != nil && e != io.EOF {
		t.Fatal(e)
	}
	feature, e := parquetFeature(rows[0].(map[string]any), pf.Schema())
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(feature)
	var f overtureFeature
	if e = json.Unmarshal(b, &f.feature); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(f.Properties, &f.Props); e != nil {
		t.Fatal(e)
	}
	f.Raw = b
	record, e := overtureRecord(f, "division", "v1")
	if e != nil {
		t.Fatal(e)
	}
	if string(record.Attributes["aliases"]) != `["Newport","뉴포트"]` {
		t.Fatal("names encoded as binary", string(record.Attributes["aliases"]))
	}
	if f.Geometry.Coordinates != [2]float64{-71.31, 41.49} {
		t.Fatal("WKB order", f.Geometry)
	}
	if !strings.Contains(string(b), "0.8999999761581421") {
		t.Fatal("lost float32 source precision", string(b))
	}
}
func TestParquetLineString(t *testing.T) {
	type row struct {
		ID       string `parquet:"id"`
		Geometry []byte `parquet:"geometry"`
		Subtype  string `parquet:"subtype"`
	}
	wkb := make([]byte, 9+2*16)
	wkb[0] = 1
	binary.LittleEndian.PutUint32(wkb[1:], 2)
	binary.LittleEndian.PutUint32(wkb[5:], 2)
	for i, point := range [][2]float64{{-71.33, 41.47}, {-71.29, 41.51}} {
		offset := 9 + i*16
		binary.LittleEndian.PutUint64(wkb[offset:], math.Float64bits(point[0]))
		binary.LittleEndian.PutUint64(wkb[offset+8:], math.Float64bits(point[1]))
	}
	var buf bytes.Buffer
	writer := parquet.NewGenericWriter[row](&buf)
	if _, e := writer.Write([]row{{ID: "segment", Geometry: wkb, Subtype: "road"}}); e != nil {
		t.Fatal(e)
	}
	if e := writer.Close(); e != nil {
		t.Fatal(e)
	}
	pf, e := parquet.OpenFile(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if e != nil {
		t.Fatal(e)
	}
	r := parquet.NewGenericReader[any](pf)
	defer r.Close()
	rows := make([]any, 1)
	if _, e = r.Read(rows); e != nil && e != io.EOF {
		t.Fatal(e)
	}
	feature, e := parquetFeature(rows[0].(map[string]any), pf.Schema())
	if e != nil {
		t.Fatal(e)
	}
	geometry := feature["geometry"].(map[string]any)
	if geometry["type"] != "LineString" || !reflect.DeepEqual(geometry["coordinates"], [][2]float64{{-71.33, 41.47}, {-71.29, 41.51}}) {
		t.Fatalf("unexpected geometry: %#v", geometry)
	}
}
func TestRangeReaderAndDownloadPublication(t *testing.T) {
	data := []byte("PAR1synthetic immutable parquet content")
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	gets := 0
	ignoreRange := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		if r.Method == "GET" {
			gets++
			if r.Header.Get("Range") != "" && r.Header.Get("If-Match") != `"v1"` {
				t.Error("missing If-Match")
			}
		}
		if ignoreRange && r.Method == "GET" {
			w.Write(data)
			return
		}
		http.ServeContent(w, r, "data", time.Time{}, bytes.NewReader(data))
	}))
	defer server.Close()
	r, e := openRange(context.Background(), server.URL)
	if e != nil {
		t.Fatal(e)
	}
	p := make([]byte, 4)
	if _, e = r.ReadAt(p, 0); e != nil || string(p) != "PAR1" {
		t.Fatal(string(p), e)
	}
	if _, e = r.ReadAt(p, 4); e != nil || gets != 1 {
		t.Fatal("cache missed", e, gets)
	}
	ignoreRange = true
	bad, e := openRange(context.Background(), server.URL)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = bad.ReadAt(p, 0); e == nil {
		t.Fatal("accepted server ignoring Range")
	}
	ignoreRange = false
	path := filepath.Join(t.TempDir(), "download")
	if e = download(context.Background(), server.URL, path, strings.Repeat("0", 64)); e == nil {
		t.Fatal("accepted checksum mismatch")
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("published corrupt download")
	}
	if e = download(context.Background(), server.URL, path, digest); e != nil {
		t.Fatal(e)
	}
	before := gets
	if e = download(context.Background(), server.URL, path, digest); e != nil || gets != before {
		t.Fatal("verified cache made network request", e)
	}
}
