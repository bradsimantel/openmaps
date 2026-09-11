package compact

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/parquet-go/parquet-go"

	"openmaps/internal/geocoding"
	"openmaps/internal/importer/addressdata"
	"openmaps/internal/places"
)

type Store struct {
	db            *sql.DB
	entities      *os.File
	entityParquet *parquet.File
	sources       *os.File
	provenance    *os.File
	bounds        [4]float64
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

// Open verifies all checksums before exposing the read-only SQLite index and
// Parquet readers.
func Open(path string) (_ *Store, err error) {
	manifest, err := Verify(path)
	if err != nil {
		return nil, err
	}
	rows := map[string]int{}
	for _, file := range manifest.Files {
		rows[file.Name] = file.Rows
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
		if pf.NumRows() != int64(rows[name]) {
			f.Close()
			return nil, nil, fmt.Errorf("%s row count: got %d want %d", name, pf.NumRows(), rows[name])
		}
		return f, pf, nil
	}
	s := &Store{}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	if s.entities, s.entityParquet, err = openParquet(EntitiesName); err != nil {
		return nil, err
	}
	if s.sources, _, err = openParquet(SourcesName); err != nil {
		return nil, err
	}
	if s.provenance, _, err = openParquet(ProvenanceName); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.Join(path, IndexName)}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	if s.db, err = sql.Open("sqlite", u.String()); err != nil {
		return nil, err
	}
	s.db.SetMaxOpenConns(4)
	var version, rawManifest string
	if err = s.db.QueryRow("SELECT value FROM metadata WHERE key='schema_version'").Scan(&version); err != nil || version != "1" {
		return nil, fmt.Errorf("compact index missing or unsupported schema: %v", err)
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
		return nil, fmt.Errorf("compact index has invalid source bounds")
	}
	copy(s.bounds[:], scope.BBox)
	return s, nil
}

func (s *Store) Close() error {
	var first error
	closers := []io.Closer{}
	if s.db != nil {
		closers = append(closers, s.db)
	}
	if s.entities != nil {
		closers = append(closers, s.entities)
	}
	if s.sources != nil {
		closers = append(closers, s.sources)
	}
	if s.provenance != nil {
		closers = append(closers, s.provenance)
	}
	for _, closer := range closers {
		if err := closer.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func entityFromRow(row entityRow) (places.Entity, error) {
	e := places.Entity{ID: row.ID, Kind: row.Kind, Name: row.Name, Address: row.Address, Website: row.Website, Subtype: row.Subtype, Location: places.Location{Lat: row.Lat, Lng: row.Lng}}
	if err := json.Unmarshal([]byte(row.Attributions), &e.Attributions); err != nil {
		return e, err
	}
	return e, nil
}

func (s *Store) Details(ctx context.Context, id string) (places.Entity, error) {
	var group, row int
	if err := s.db.QueryRowContext(ctx, "SELECT entity_row_group,entity_row FROM entity_locator WHERE id=?", id).Scan(&group, &row); err != nil {
		return places.Entity{}, err
	}
	if group < 0 || group >= len(s.entityParquet.RowGroups()) {
		return places.Entity{}, fmt.Errorf("invalid entity row group for %s", id)
	}
	reader := parquet.NewGenericRowGroupReader[entityRow](s.entityParquet.RowGroups()[group])
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

func (s *Store) Autocomplete(ctx context.Context, input string) ([]places.Entity, error) {
	normalized := places.Normalize(input)
	if normalized == "" {
		return []places.Entity{}, nil
	}
	if !strings.Contains(normalized, " ") && utf8.RuneCountInString(normalized) == 2 {
		rows, err := s.db.QueryContext(ctx, `SELECT e.id,e.kind,e.name,e.address,e.subtype
 FROM short_prefix_head h JOIN search_entities e ON e.id=h.entity_id
 WHERE h.prefix=? ORDER BY h.rank`, normalized)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []places.Entity{}
		for rows.Next() {
			var entity places.Entity
			if err = rows.Scan(&entity.ID, &entity.Kind, &entity.Name, &entity.Address, &entity.Subtype); err != nil {
				return nil, err
			}
			out = append(out, entity)
		}
		return out, rows.Err()
	}
	return queryFTS(ctx, s.db, normalized)
}

func (s *Store) Evidence(ctx context.Context, id string) (Evidence, error) {
	entity, err := s.Details(ctx, id)
	if err != nil {
		return Evidence{}, err
	}
	var sourceStart, sourceCount, provenanceStart, provenanceCount int
	if err = s.db.QueryRowContext(ctx, `SELECT source_start,source_count,provenance_start,provenance_count FROM entity_locator WHERE id=?`, id).Scan(&sourceStart, &sourceCount, &provenanceStart, &provenanceCount); err != nil {
		return Evidence{}, err
	}
	sourceRows := make([]sourceRow, sourceCount)
	if err = readAt(s.sources, sourceStart, sourceRows); err != nil {
		return Evidence{}, err
	}
	provenanceRows := make([]provenanceRow, provenanceCount)
	if err = readAt(s.provenance, provenanceStart, provenanceRows); err != nil {
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
	query, out, err := geocoding.ParseForward(input)
	if err != nil {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT entity_id,lat,lng,context FROM address_lookup WHERE address_key=? ORDER BY entity_id`, query.Key)
	if err != nil {
		return out, err
	}
	candidates := []addressCandidate{}
	for rows.Next() {
		candidate, err := scanAddress(rows)
		if err != nil {
			rows.Close()
			return out, err
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
		result, err := s.materializeAddress(ctx, candidate)
		if err != nil {
			return out, err
		}
		result.Partial = query.Partial
		out.Results = append(out.Results, result)
	}
	if len(out.Results) > 0 {
		out.Outcome = "matched"
	}
	if len(out.Results) > 1 {
		out.Outcome = "ambiguous"
	}
	return out, nil
}

func validPoint(p places.Location) bool {
	return !math.IsNaN(p.Lat) && !math.IsNaN(p.Lng) && !math.IsInf(p.Lat, 0) && !math.IsInf(p.Lng, 0) && p.Lat >= -90 && p.Lat <= 90 && p.Lng >= -180 && p.Lng <= 180
}

func (s *Store) contains(p places.Location) bool {
	return p.Lng >= s.bounds[0] && p.Lng <= s.bounds[2] && p.Lat >= s.bounds[1] && p.Lat <= s.bounds[3]
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
	rows, err := s.db.QueryContext(ctx, `SELECT a.entity_id,a.lat,a.lng,a.context
 FROM address_rtree r JOIN address_lookup a ON a.rowid=r.rowid
 WHERE r.min_lng BETWEEN ? AND ? AND r.min_lat BETWEEN ? AND ? ORDER BY a.entity_id`,
		point.Lng-lngDelta, point.Lng+lngDelta, point.Lat-latDelta, point.Lat+latDelta)
	if err != nil {
		return out, err
	}
	nearest := math.Inf(1)
	for rows.Next() {
		candidate, err := scanAddress(rows)
		if err != nil {
			rows.Close()
			return out, err
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
		result, err := s.materializeAddress(ctx, candidate)
		if err != nil {
			return out, err
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
