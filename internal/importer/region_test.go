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
	"google.golang.org/protobuf/encoding/protowire"
)

func testFeature(id string, properties map[string]any) map[string]any {
	properties["id"] = id
	properties["sources"] = []any{map[string]any{"dataset": "synthetic", "record_id": id, "property": ""}}
	return map[string]any{"type": "Feature", "geometry": map[string]any{"type": "Point", "coordinates": []float64{-71.31, 41.49}}, "properties": properties}
}
func regionFixture(t *testing.T) (Manifest, string) {
	t.Helper()
	dir := t.TempDir()
	m := Manifest{Schema: 1, Region: "synthetic", BBox: [4]float64{-71.33, 41.47, -71.29, 41.51}, CoordinateOrder: "longitude,latitude"}
	fixtures := map[string][]any{
		"place":    {testFeature("bakery", map[string]any{"names": map[string]any{"primary": "Fixture Bakery", "common": [][]string{{"en", "Bread Shop"}, {"fr", "Boulangerie"}}}, "addresses": []any{map[string]any{"freeform": "26 Example St", "postcode": "02840"}}, "websites": []string{"https://example.org"}}), testFeature("phantom", map[string]any{"names": map[string]any{"primary": "Phantom Business"}, "addresses": []any{map[string]any{"freeform": "99 Phantom Street", "postcode": "02840"}}})},
		"address":  {testFeature("address-a", map[string]any{"number": "26", "street": "Example Street", "postcode": "02840", "country": "US"})},
		"division": {testFeature("town", map[string]any{"names": map[string]any{"primary": "Fixture Town"}, "subtype": "locality", "parent_division_id": "absent-country"})},
	}
	for _, kind := range []string{"place", "address", "division"} {
		name := kind + ".geojson"
		if e := WriteJSON(filepath.Join(dir, name), map[string]any{"type": "FeatureCollection", "features": fixtures[kind]}); e != nil {
			t.Fatal(e)
		}
		hash, _ := Checksum(filepath.Join(dir, name))
		m.Inputs = append(m.Inputs, Input{File: name, SHA256: hash, Release: "fixture-v1", URL: "s3://fixture/type=" + kind + "/", Attribution: "synthetic"})
	}
	pbf := filepath.Join(dir, "streets.osm.pbf")
	if e := os.WriteFile(pbf, tinyPBF(false), 0600); e != nil {
		t.Fatal(e)
	}
	hash, _ := Checksum(pbf)
	m.Inputs = append(m.Inputs, Input{File: filepath.Base(pbf), SHA256: hash, Release: "fixture-v1", URL: "https://example.org/streets.osm.pbf", Attribution: "synthetic"})
	return m, dir
}
func TestPrepareProviderRecordsAndAmbiguity(t *testing.T) {
	m, dir := regionFixture(t)
	b, a, e := Prepare(context.Background(), m, dir, map[string]string{})
	if e != nil {
		t.Fatal(e)
	}
	if len(b.Records) != 5 || len(b.Relationships) != 1 || b.Relationships[0].To != "overture:address:address-a" {
		t.Fatalf("unexpected import: %+v", b)
	}
	if a["osm_address_nodes"] != 2 || a["osm_address_labels_also_in_overture"] != 1 {
		t.Fatalf("business-only address inflated overlap: %v", a)
	}
	var bakery Record
	for _, r := range b.Records {
		if r.SourceID == "bakery" {
			bakery = r
		}
	}
	if !bytes.Contains(bakery.Raw, []byte(`"record_id":"bakery"`)) || string(bakery.Attributes["aliases"]) != `["Boulangerie","Bread Shop"]` {
		t.Fatalf("lost upstream identity/aliases: %+v", bakery)
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
	if e != nil || len(b.Relationships) != 0 || len(b.Records) != 6 || a["ambiguous_business_addresses"] != 1 {
		t.Fatal("ambiguity handling", a, e)
	}
}
func TestStreetMissingNodesFailAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.osm.pbf")
	if e := os.WriteFile(path, tinyPBF(true), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e := readStreets(context.Background(), path, "v1", [4]float64{-72, 41, -71, 42}); e == nil || !strings.Contains(e.Error(), "missing node") {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, e := readStreets(ctx, path, "v1", [4]float64{-72, 41, -71, 42}); e == nil {
		t.Fatal("ignored cancellation")
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

// tinyPBF emits a deterministic, synthetic fixture using protobuf wire helpers;
// it is not an alternate production PBF parser or writer.
func tinyPBF(missing bool) []byte {
	fieldBytes := func(n protowire.Number, b []byte) []byte {
		return protowire.AppendBytes(protowire.AppendTag(nil, n, protowire.BytesType), b)
	}
	varint := func(n protowire.Number, v uint64) []byte {
		return protowire.AppendVarint(protowire.AppendTag(nil, n, protowire.VarintType), v)
	}
	packed := func(n protowire.Number, values ...uint64) []byte {
		var b []byte
		for _, v := range values {
			b = protowire.AppendVarint(b, v)
		}
		return fieldBytes(n, b)
	}
	block := func(kind string, raw []byte) []byte {
		blob := fieldBytes(1, raw)
		header := append(fieldBytes(1, []byte(kind)), varint(3, uint64(len(blob)))...)
		out := make([]byte, 4)
		binary.BigEndian.PutUint32(out, uint32(len(header)))
		return append(append(out, header...), blob...)
	}
	var table []byte
	for _, s := range []string{"", "highway", "residential", "name", "Example Street", "addr:housenumber", "26", "addr:street", "99", "Phantom Street"} {
		table = append(table, fieldBytes(1, []byte(s))...)
	}
	ids, lats, lons := []uint64{protowire.EncodeZigZag(1)}, []uint64{protowire.EncodeZigZag(414900000)}, []uint64{protowire.EncodeZigZag(-713100000)}
	tags := []uint64{5, 6, 7, 4, 0}
	if !missing {
		ids = append(ids, protowire.EncodeZigZag(1))
		lats = append(lats, 0)
		lons = append(lons, protowire.EncodeZigZag(1))
		tags = append(tags, 5, 8, 7, 9, 0)
	}
	dense := packed(1, ids...)
	dense = append(dense, packed(8, lats...)...)
	dense = append(dense, packed(9, lons...)...)
	dense = append(dense, packed(10, tags...)...)
	nodes := fieldBytes(2, dense)
	way := varint(1, 1)
	way = append(way, packed(2, 1, 3)...)
	way = append(way, packed(3, 2, 4)...)
	way = append(way, packed(8, protowire.EncodeZigZag(1), protowire.EncodeZigZag(1))...)
	primitive := fieldBytes(1, table)
	primitive = append(primitive, fieldBytes(2, nodes)...)
	primitive = append(primitive, fieldBytes(2, fieldBytes(3, way))...)
	return append(block("OSMHeader", append(fieldBytes(4, []byte("OsmSchema-V0.6")), fieldBytes(4, []byte("DenseNodes"))...)), block("OSMData", primitive)...)
}
