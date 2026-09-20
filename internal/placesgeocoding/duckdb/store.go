package duckdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/parquet-go/parquet-go"

	"openmaps/internal/geocoding"
	"openmaps/internal/importer"
	"openmaps/internal/importer/addressdata"
	"openmaps/internal/places"
)

type Store struct {
	db            *sql.DB
	files         map[string]*os.File
	entityParquet map[string]*parquet.File
	sourceParquet map[string]*parquet.File
	areaParents   map[string][]string
	bounds        [4]float64

	primaryCandidateCacheMu sync.RWMutex
	primaryCandidateCache   map[string][]primaryCandidate
}

type Source struct {
	EntityID   string
	SourceKey  string
	Source     string
	SourceID   string
	Release    string
	Priority   int
	Attributes json.RawMessage
	Paths      json.RawMessage
	Raw        json.RawMessage
}

type AttributeProvenance struct {
	EntityID, Attribute, SourceKey, SourcePath string
}

type Evidence struct {
	Entity     places.Entity
	Sources    []Source
	Attributes []AttributeProvenance
}

// Open verifies every generation checksum before exposing DuckDB or Parquet.
func Open(path string) (_ *Store, err error) {
	store, _, err := OpenWithReference(path, nil)
	return store, err
}

// OpenWithReference verifies and opens one generation and returns the manifest
// reference established by that same verification pass. The observer receives
// independent validation and open phase durations.
func OpenWithReference(path string, observe func(string, time.Duration)) (_ *Store, reference Reference, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, reference, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, reference, err
	}
	started := time.Now()
	digest, err := importer.Checksum(filepath.Join(abs, ManifestName))
	if err != nil {
		return nil, reference, err
	}
	checksumElapsed := time.Since(started)
	manifest, err := VerifyObserved(abs, observe)
	if err != nil {
		return nil, reference, err
	}
	started = time.Now()
	verifiedDigest, err := importer.Checksum(filepath.Join(abs, ManifestName))
	if err != nil {
		return nil, reference, err
	}
	if verifiedDigest != digest {
		return nil, reference, fmt.Errorf("manifest changed during verification")
	}
	checksumElapsed += time.Since(started)
	if observe != nil {
		observe("reference_manifest_checksum", checksumElapsed)
	}
	store, err := openVerifiedObserved(abs, manifest, observe)
	if err != nil {
		return nil, reference, err
	}
	return store, Reference{Path: abs, SHA256: digest}, nil
}

// openVerified opens an artifact whose checksums and catalog have already been
// verified by the caller. Snapshot activation uses this to avoid reading every
// immutable file twice before a generation becomes visible.
func openVerified(path string, manifest Manifest) (_ *Store, err error) {
	return openVerifiedObserved(path, manifest, nil)
}

func openVerifiedObserved(path string, manifest Manifest, observe func(string, time.Duration)) (_ *Store, err error) {
	phaseStarted := time.Now()
	phase := func(name string) {
		if observe != nil {
			observe(name, time.Since(phaseStarted))
		}
		phaseStarted = time.Now()
	}
	counts := map[string]int{}
	roles := map[string]string{}
	for _, file := range manifest.Files {
		counts[file.Name] = file.Rows
		roles[file.Name] = file.Role
	}
	openParquet := func(name string) (*os.File, *parquet.File, error) {
		f, e := os.Open(filepath.Join(path, name))
		if e != nil {
			return nil, nil, e
		}
		stat, e := f.Stat()
		if e != nil {
			f.Close()
			return nil, nil, e
		}
		pf, e := parquet.OpenFile(f, stat.Size())
		if e != nil {
			f.Close()
			return nil, nil, e
		}
		if pf.NumRows() != int64(counts[name]) {
			f.Close()
			return nil, nil, fmt.Errorf("%s row count: got %d want %d", name, pf.NumRows(), counts[name])
		}
		return f, pf, nil
	}
	s := &Store{
		files:                 map[string]*os.File{},
		entityParquet:         map[string]*parquet.File{},
		sourceParquet:         map[string]*parquet.File{},
		primaryCandidateCache: map[string][]primaryCandidate{},
	}
	defer func() {
		if err != nil {
			_ = s.Close()
		}
	}()
	for name, role := range roles {
		if role != "entities" && role != "sources" && role != "provenance" {
			continue
		}
		file, pf, openErr := openParquet(name)
		if openErr != nil {
			return nil, openErr
		}
		s.files[name] = file
		if role == "entities" {
			s.entityParquet[name] = pf
		} else if role == "sources" {
			s.sourceParquet[name] = pf
		}
	}
	phase("open_parquet_metadata")
	if s.db, err = openDatabase(filepath.Join(path, IndexName)); err != nil {
		return nil, err
	}
	phase("open_catalog")
	var version, rawManifest string
	if err = s.db.QueryRow("SELECT value FROM metadata WHERE key='schema_version'").Scan(&version); err != nil || version != "2" {
		return nil, fmt.Errorf("DuckDB index missing or unsupported schema: %v", err)
	}
	if err = s.db.QueryRow("SELECT value FROM metadata WHERE key='source_manifest'").Scan(&rawManifest); err != nil {
		return nil, err
	}
	var scope struct {
		BBox []float64 `json:"bbox"`
	}
	if err = json.Unmarshal([]byte(rawManifest), &scope); err != nil || len(scope.BBox) != 4 ||
		!validPoint(places.Location{Lat: scope.BBox[1], Lng: scope.BBox[0]}) ||
		!validPoint(places.Location{Lat: scope.BBox[3], Lng: scope.BBox[2]}) ||
		scope.BBox[0] >= scope.BBox[2] || scope.BBox[1] >= scope.BBox[3] {
		return nil, fmt.Errorf("DuckDB index has invalid source bounds")
	}
	copy(s.bounds[:], scope.BBox)
	phase("open_catalog_metadata")
	if s.areaParents, err = loadAreaParents(s.db, path); err != nil {
		return nil, err
	}
	phase("open_parent_area_graph")
	return s, nil
}

func loadAreaParents(db *sql.DB, path string) (map[string][]string, error) {
	rows, err := db.Query(`SELECT from_id,to_id FROM read_parquet(?)
WHERE kind='parent_area' ORDER BY from_id,to_id`, filepath.Join(path, "relationships*.parquet"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parents := map[string][]string{}
	for rows.Next() {
		var child, parent string
		if err = rows.Scan(&child, &parent); err != nil {
			return nil, err
		}
		parents[child] = append(parents[child], parent)
	}
	return parents, rows.Err()
}

func (s *Store) Close() error {
	var first error
	closers := []io.Closer{}
	if s.db != nil {
		closers = append(closers, s.db)
	}
	for _, file := range s.files {
		closers = append(closers, file)
	}
	for _, closer := range closers {
		if err := closer.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func entityFromRow(row entityRow) (places.Entity, error) {
	entity := places.Entity{
		ID: row.ID, Kind: row.Kind, Name: row.Name, Address: row.Address,
		Website: row.Website, Subtype: row.Subtype,
		Location: places.Location{Lat: row.Lat, Lng: row.Lng},
	}
	canonicalizeEntity(&entity)
	if err := json.Unmarshal([]byte(row.Attributions), &entity.Attributions); err != nil {
		return entity, err
	}
	return entity, nil
}

func canonicalizeEntity(entity *places.Entity) {
	// National source names spell out saint even where the ordinary US locality
	// label uses the abbreviation. Keep autocomplete and details consistent.
	if entity.Kind == "area" && entity.Subtype == "locality" && strings.HasPrefix(entity.Name, "Saint ") {
		entity.Name = "St. " + strings.TrimPrefix(entity.Name, "Saint ")
	}
}

func (s *Store) Details(ctx context.Context, id string) (places.Entity, error) {
	var fileName string
	var group, row int
	if err := s.db.QueryRowContext(ctx, "SELECT entity_file,entity_row_group,entity_row FROM entity_locator WHERE id=?", id).Scan(&fileName, &group, &row); err != nil {
		return places.Entity{}, err
	}
	entityFile := s.entityParquet[fileName]
	if entityFile == nil || group < 0 || group >= len(entityFile.RowGroups()) {
		return places.Entity{}, fmt.Errorf("invalid entity row group for %s", id)
	}
	reader := parquet.NewGenericRowGroupReader[entityRow](entityFile.RowGroups()[group])
	defer reader.Close()
	if err := reader.SeekToRow(int64(row)); err != nil {
		return places.Entity{}, err
	}
	values := make([]entityRow, 1)
	if n, err := reader.Read(values); n != 1 || err != nil && err != io.EOF {
		return places.Entity{}, fmt.Errorf("read entity %s: rows=%d: %w", id, n, err)
	}
	if values[0].ID != id {
		return places.Entity{}, fmt.Errorf("entity locator mismatch: got %s want %s", values[0].ID, id)
	}
	return entityFromRow(values[0])
}

func (s *Store) Evidence(ctx context.Context, id string) (Evidence, error) {
	entity, err := s.Details(ctx, id)
	if err != nil {
		return Evidence{}, err
	}
	var sourceFile, provenanceFile string
	var sourceStart, sourceCount, provenanceStart, provenanceCount int
	if err = s.db.QueryRowContext(ctx, `SELECT source_file,source_start,source_count,provenance_file,provenance_start,provenance_count
FROM entity_locator WHERE id=?`, id).Scan(&sourceFile, &sourceStart, &sourceCount, &provenanceFile, &provenanceStart, &provenanceCount); err != nil {
		return Evidence{}, err
	}
	sourceRows := make([]sourceRow, sourceCount)
	sourceHandle := s.files[sourceFile]
	provenanceHandle := s.files[provenanceFile]
	if sourceHandle == nil || provenanceHandle == nil {
		return Evidence{}, fmt.Errorf("evidence locator references missing shard for %s", id)
	}
	if err = readAt(sourceHandle, sourceStart, sourceRows); err != nil {
		return Evidence{}, err
	}
	provenanceRows := make([]provenanceRow, provenanceCount)
	if err = readAt(provenanceHandle, provenanceStart, provenanceRows); err != nil {
		return Evidence{}, err
	}
	out := Evidence{Entity: entity, Sources: make([]Source, 0, len(sourceRows)), Attributes: make([]AttributeProvenance, 0, len(provenanceRows))}
	for _, row := range sourceRows {
		if row.EntityID != id {
			return Evidence{}, fmt.Errorf("source locator mismatch for %s", id)
		}
		out.Sources = append(out.Sources, Source{
			EntityID: row.EntityID, SourceKey: row.SourceKey, Source: row.Source,
			SourceID: row.SourceID, Release: row.Release, Priority: int(row.Priority),
			Attributes: json.RawMessage(row.Attributes), Paths: json.RawMessage(row.Paths), Raw: json.RawMessage(row.Raw),
		})
	}
	for _, row := range provenanceRows {
		if row.EntityID != id {
			return Evidence{}, fmt.Errorf("provenance locator mismatch for %s", id)
		}
		out.Attributes = append(out.Attributes, AttributeProvenance{row.EntityID, row.Attribute, row.SourceKey, row.SourcePath})
	}
	return out, nil
}

func readAt[T any](file *os.File, start int, values []T) error {
	reader := parquet.NewGenericReader[T](file)
	defer reader.Close()
	if err := reader.SeekToRow(int64(start)); err != nil {
		return err
	}
	n, err := reader.Read(values)
	if n != len(values) || err != nil && err != io.EOF {
		return fmt.Errorf("read Parquet span: rows=%d/%d: %w", n, len(values), err)
	}
	return nil
}

type addressCandidate struct {
	id, context string
	location    places.Location
}

func scanAddress(row interface{ Scan(...any) error }) (addressCandidate, error) {
	var candidate addressCandidate
	err := row.Scan(&candidate.id, &candidate.location.Lat, &candidate.location.Lng, &candidate.context)
	return candidate, err
}

func (s *Store) materializeAddress(ctx context.Context, candidate addressCandidate) (geocoding.Result, error) {
	evidence, err := s.Evidence(ctx, candidate.id)
	if err != nil {
		return geocoding.Result{}, err
	}
	var sourceKey string
	for _, provenance := range evidence.Attributes {
		if provenance.Attribute == "address" {
			sourceKey = provenance.SourceKey
			break
		}
	}
	var components places.AddressComponents
	found := false
	for _, source := range evidence.Sources {
		if source.SourceKey == sourceKey {
			found = true
			components, err = addressdata.Components(source.Source, string(source.Raw), evidence.Entity.Name, evidence.Entity.Address)
			if err != nil {
				return geocoding.Result{}, err
			}
			break
		}
	}
	if sourceKey != "" && !found {
		return geocoding.Result{}, fmt.Errorf("address provenance source missing for %s", candidate.id)
	}
	return geocoding.Result{Entity: evidence.Entity, Components: components}, nil
}

func (s *Store) Forward(ctx context.Context, input string) (geocoding.Response, error) {
	return s.ForwardWithBias(ctx, input, nil)
}

// ForwardWithBias retains every exact match and only changes their order. A
// candidate outside the viewport remains eligible.
func (s *Store) ForwardWithBias(ctx context.Context, input string, boundsBias *places.Viewport) (geocoding.Response, error) {
	query, out, err := geocoding.ParseForward(input)
	if err != nil {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT entity_id,lat,lng,context
FROM address_lookup WHERE address_key=? ORDER BY entity_id`, query.Key)
	if err != nil {
		return out, err
	}
	candidates := []addressCandidate{}
	for rows.Next() {
		candidate, scanErr := scanAddress(rows)
		if scanErr != nil {
			rows.Close()
			return out, scanErr
		}
		if geocoding.ContextMatches(candidate.context, query.Context) {
			candidates = append(candidates, candidate)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	for _, candidate := range candidates {
		result, materializeErr := s.materializeAddress(ctx, candidate)
		if materializeErr != nil {
			return out, materializeErr
		}
		result.Partial = query.Partial
		out.Results = append(out.Results, result)
	}
	if boundsBias != nil {
		center := boundsBias.Center()
		sort.SliceStable(out.Results, func(i, j int) bool {
			left, right := out.Results[i].Entity.Location, out.Results[j].Entity.Location
			leftInside, rightInside := boundsBias.Contains(left), boundsBias.Contains(right)
			if leftInside != rightInside {
				return leftInside
			}
			return places.DistanceMeters(center, left) < places.DistanceMeters(center, right)
		})
	}
	if len(out.Results) > 0 {
		out.Outcome = "matched"
	}
	if len(out.Results) > 1 {
		out.Outcome = "ambiguous"
	}
	return out, nil
}

func validPoint(point places.Location) bool {
	return !math.IsNaN(point.Lat) && !math.IsNaN(point.Lng) && !math.IsInf(point.Lat, 0) && !math.IsInf(point.Lng, 0) && point.Lat >= -90 && point.Lat <= 90 && point.Lng >= -180 && point.Lng <= 180
}

func (s *Store) contains(point places.Location) bool {
	return point.Lng >= s.bounds[0] && point.Lng <= s.bounds[2] && point.Lat >= s.bounds[1] && point.Lat <= s.bounds[3]
}

func gridCell(latCell, lngCell int64) int64 { return latCell*360001 + lngCell }

func reverseCells(point places.Location, latDelta, lngDelta float64) ([]int64, int64, int64) {
	minLat := int64(math.Floor((math.Max(-90, point.Lat-latDelta) + 90) * 1000))
	maxLat := int64(math.Floor((math.Min(90, point.Lat+latDelta) + 90) * 1000))
	minLng := int64(math.Floor((math.Max(-180, point.Lng-lngDelta) + 180) * 1000))
	maxLng := int64(math.Floor((math.Min(180, point.Lng+lngDelta) + 180) * 1000))
	if maxLng-minLng > 1000 {
		return nil, minLat, maxLat
	}
	cells := make([]int64, 0, (maxLat-minLat+1)*(maxLng-minLng+1))
	for lat := minLat; lat <= maxLat; lat++ {
		for lng := minLng; lng <= maxLng; lng++ {
			cells = append(cells, gridCell(lat, lng))
		}
	}
	return cells, minLat, maxLat
}

func (s *Store) Reverse(ctx context.Context, point places.Location) (geocoding.Response, error) {
	out := geocoding.Response{Results: []geocoding.Result{}, Outcome: "no_nearby_address"}
	if !validPoint(point) {
		out.Outcome = "invalid_input"
		return out, fmt.Errorf("latlng must be finite latitude [-90,90],longitude [-180,180], in that order")
	}
	if !s.contains(point) {
		out.Outcome = "outside_coverage"
		return out, nil
	}
	latDelta := geocoding.ReverseLimitMeters / 111195.0
	cos := math.Abs(math.Cos(point.Lat * math.Pi / 180))
	lngDelta := 180.0
	if cos > 1e-9 {
		lngDelta = math.Min(180, latDelta/cos)
	}
	cells, minLatCell, maxLatCell := reverseCells(point, latDelta, lngDelta)
	args := []any{}
	predicate := "lat_cell BETWEEN ? AND ?"
	args = append(args, minLatCell, maxLatCell)
	if len(cells) > 0 {
		predicate = "cell_id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(cells)), ",") + ")"
		args = args[:0]
		for _, cell := range cells {
			args = append(args, cell)
		}
	}
	predicate += " AND lng BETWEEN ? AND ? AND lat BETWEEN ? AND ?"
	args = append(args, math.Max(-180, point.Lng-lngDelta), math.Min(180, point.Lng+lngDelta), math.Max(-90, point.Lat-latDelta), math.Min(90, point.Lat+latDelta))
	rows, err := s.db.QueryContext(ctx, `SELECT entity_id,lat,lng,context FROM address_spatial
WHERE `+predicate+" ORDER BY entity_id", args...)
	if err != nil {
		return out, err
	}
	nearest := math.Inf(1)
	for rows.Next() {
		candidate, scanErr := scanAddress(rows)
		if scanErr != nil {
			rows.Close()
			return out, scanErr
		}
		distance := geocoding.DistanceMeters(point, candidate.location)
		if distance <= geocoding.ReverseLimitMeters {
			out.Results = append(out.Results, geocoding.Result{Entity: places.Entity{ID: candidate.id, Location: candidate.location}, DistanceMeters: &distance})
			nearest = math.Min(nearest, distance)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	kept := out.Results[:0]
	for _, result := range out.Results {
		if *result.DistanceMeters <= nearest+0.001 {
			kept = append(kept, result)
		}
	}
	out.Results = kept
	for i := range out.Results {
		candidate := addressCandidate{id: out.Results[i].Entity.ID, location: out.Results[i].Entity.Location}
		result, materializeErr := s.materializeAddress(ctx, candidate)
		if materializeErr != nil {
			return out, materializeErr
		}
		result.DistanceMeters = out.Results[i].DistanceMeters
		out.Results[i] = result
	}
	sort.Slice(out.Results, func(i, j int) bool {
		a, b := out.Results[i], out.Results[j]
		if *a.DistanceMeters != *b.DistanceMeters {
			return *a.DistanceMeters < *b.DistanceMeters
		}
		return a.Entity.ID < b.Entity.ID
	})
	if len(out.Results) > 0 {
		out.Outcome = "matched"
	}
	if len(out.Results) > 1 {
		out.Outcome = "ambiguous"
	}
	return out, nil
}
