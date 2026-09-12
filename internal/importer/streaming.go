package importer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/format"

	"openmaps/internal/places"
)

// PreparedSink accepts bounded batches of provider-independent preparation
// output. The DuckDB generation builder implements this interface.
type PreparedSink interface {
	WriteRecords(context.Context, []Record) error
	WriteRelationships(context.Context, []Relationship) error
	WriteRejections(context.Context, []Rejection) error
}

type AssetPreflight struct {
	Kind                  string `json:"kind"`
	Assets                int    `json:"assets"`
	AssetBytes            int64  `json:"asset_bytes"`
	RowGroupsRead         int    `json:"row_groups_read"`
	RowsRead              int64  `json:"rows_read"`
	UncompressedBytesRead int64  `json:"uncompressed_bytes_read"`
	CandidateRowGroups    int    `json:"candidate_row_groups"`
	CandidateRows         int64  `json:"candidate_rows"`
	CandidateBytes        int64  `json:"candidate_uncompressed_bytes"`
	AssetsSHA256          string `json:"assets_sha256"`
	AssetVersionsSHA256   string `json:"asset_versions_sha256"`
}

type StreamingPreflight struct {
	Region                      string           `json:"region"`
	Release                     string           `json:"release"`
	Inputs                      []AssetPreflight `json:"inputs"`
	CandidateRows               int64            `json:"candidate_rows"`
	RowsRead                    int64            `json:"rows_read"`
	EstimatedPeakWorkspaceBytes int64            `json:"estimated_peak_workspace_bytes"`
}

type StreamingAudit struct {
	Region            string             `json:"region"`
	BatchRows         int                `json:"batch_rows"`
	SourceRows        map[string]int64   `json:"source_rows"`
	RemoteBytesRead   int64              `json:"remote_bytes_read"`
	ImportedCounts    map[string]int64   `json:"imported_counts"`
	RejectedCounts    map[string]int64   `json:"rejected_counts"`
	Relationships     map[string]int64   `json:"relationships"`
	MaxSubmittedBatch int                `json:"max_submitted_batch"`
	PhaseSeconds      map[string]float64 `json:"phase_seconds"`
}

// PreflightStreaming reads only the pinned catalog, remote Parquet footers and
// row-group metadata. Candidate counts deliberately overestimate rows because
// exact geometry and country/state filters are applied during streaming.
func PreflightStreaming(ctx context.Context, m Manifest, dataDir string) (StreamingPreflight, error) {
	var out StreamingPreflight
	if m.Schema != 2 || m.Streaming == nil {
		return out, fmt.Errorf("streaming manifest required")
	}
	out.Region = m.Region
	for _, input := range m.Inputs {
		assets, err := streamingAssets(filepath.Join(dataDir, m.Catalog.File), input, m.Scopes)
		if err != nil {
			return out, err
		}
		metric := AssetPreflight{Kind: sourceKind(input.URL), Assets: len(assets), AssetsSHA256: assetDigest(assets)}
		if metric.AssetsSHA256 != input.AssetsSHA256 {
			return out, fmt.Errorf("%s asset-set checksum mismatch: got %s", metric.Kind, metric.AssetsSHA256)
		}
		versionHash := sha256.New()
		for _, asset := range assets {
			remote, err := openRange(ctx, asset)
			if err != nil {
				return out, err
			}
			metric.AssetBytes += remote.size
			fmt.Fprintf(versionHash, "%s\t%d\t%s\n", asset, remote.size, remote.etag)
			file, err := parquet.OpenFile(remote, remote.size, parquet.SkipPageIndex(true), parquet.SkipBloomFilters(true))
			if err != nil {
				return out, err
			}
			for _, group := range file.Metadata().RowGroups {
				candidate := overlapsAnyStatsFormat(group, scopeBounds(m.Scopes))
				if metric.Kind == "division" || candidate {
					metric.RowGroupsRead++
					metric.RowsRead += group.NumRows
					metric.UncompressedBytesRead += group.TotalByteSize
				}
				if candidate {
					metric.CandidateRowGroups++
					metric.CandidateRows += group.NumRows
					metric.CandidateBytes += group.TotalByteSize
				}
			}
		}
		metric.AssetVersionsSHA256 = hex.EncodeToString(versionHash.Sum(nil))
		if metric.AssetVersionsSHA256 != input.AssetVersionsSHA256 {
			return out, fmt.Errorf("%s asset-version checksum mismatch: got %s", metric.Kind, metric.AssetVersionsSHA256)
		}
		out.Inputs = append(out.Inputs, metric)
		out.CandidateRows += metric.CandidateRows
		out.RowsRead += metric.RowsRead
	}
	if len(m.Inputs) > 0 {
		out.Release = m.Inputs[0].Release
	}
	baseEstimate := out.RowsRead * m.Streaming.EstimatedBytesPerRow
	// The first multi-state gate observed a 1.7% filesystem high-water excess
	// over the raw coefficient. Keep a wider 10% operational margin.
	out.EstimatedPeakWorkspaceBytes = baseEstimate + baseEstimate/10
	return out, nil
}

func streamingAssets(catalog string, input Input, scopes []Scope) ([]string, error) {
	set := map[string]bool{}
	for _, scope := range scopes {
		assets, err := catalogAssets(catalog, sourceKind(input.URL), scope.BBox)
		if err != nil {
			return nil, err
		}
		for _, asset := range assets {
			if err = validateAsset(asset, input); err != nil {
				return nil, err
			}
			set[asset] = true
		}
	}
	out := make([]string, 0, len(set))
	for asset := range set {
		out = append(out, asset)
	}
	sort.Strings(out)
	return out, nil
}

func assetDigest(assets []string) string {
	h := sha256.New()
	for _, asset := range assets {
		io.WriteString(h, asset)
		io.WriteString(h, "\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}

func scopeBounds(scopes []Scope) [][4]float64 {
	out := make([][4]float64, len(scopes))
	for i := range scopes {
		out[i] = scopes[i].BBox
	}
	return out
}

func overlapsAnyStatsFormat(group format.RowGroup, bounds [][4]float64) bool {
	for _, bound := range bounds {
		if overlapsStats(group, bound) {
			return true
		}
	}
	return false
}

// PrepareStreaming range-reads pinned Overture Parquet and emits bounded,
// deterministic batches. Its only size-dependent state is a spillable DuckDB
// preparation database used for division ancestry and business/address joins.
func PrepareStreaming(ctx context.Context, m Manifest, dataDir string, sink PreparedSink) (StreamingAudit, error) {
	audit := StreamingAudit{Region: m.Region, BatchRows: m.Streaming.BatchRows, SourceRows: map[string]int64{}, ImportedCounts: map[string]int64{}, RejectedCounts: map[string]int64{}, Relationships: map[string]int64{}, PhaseSeconds: map[string]float64{}}
	temp, err := os.MkdirTemp(dataDir, ".places-stream-*")
	if err != nil {
		return audit, err
	}
	defer os.RemoveAll(temp)
	dsn := filepath.Join(temp, "prepare.duckdb") + "?threads=1&memory_limit=" + url.QueryEscape(m.Streaming.MemoryLimit) + "&preserve_insertion_order=false&temp_directory=" + url.QueryEscape(filepath.Join(temp, "spill"))
	db, err := sql.Open("duckdb", dsn)
	if err != nil {
		return audit, err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err = db.ExecContext(ctx, `SET autoinstall_known_extensions=false; SET autoload_known_extensions=false;
CREATE TABLE divisions(id VARCHAR,parent VARCHAR,inside BOOLEAN,candidate BOOLEAN,rejection VARCHAR,release VARCHAR,raw VARCHAR);
CREATE TABLE link_addresses(source_key VARCHAR,address_key VARCHAR,postcode VARCHAR,lat DOUBLE,lng DOUBLE);
CREATE TABLE link_businesses(source_key VARCHAR,address_key VARCHAR,postcode VARCHAR,lat DOUBLE,lng DOUBLE);`); err != nil {
		return audit, err
	}
	aux := newAuxStager(ctx, db, m.Streaming.BatchRows)

	records := make([]Record, 0, m.Streaming.BatchRows)
	rejections := make([]Rejection, 0, m.Streaming.BatchRows)
	flushRecords := func() error {
		audit.MaxSubmittedBatch = max(audit.MaxSubmittedBatch, len(records))
		err := sink.WriteRecords(ctx, records)
		records = records[:0]
		return err
	}
	flushRejections := func() error {
		audit.MaxSubmittedBatch = max(audit.MaxSubmittedBatch, len(rejections))
		err := sink.WriteRejections(ctx, rejections)
		rejections = rejections[:0]
		return err
	}
	addRecord := func(record Record) error {
		records = append(records, record)
		audit.ImportedCounts[record.Kind]++
		if len(records) == cap(records) {
			return flushRecords()
		}
		return nil
	}
	addRejection := func(rejection Rejection) error {
		rejections = append(rejections, rejection)
		audit.RejectedCounts[rejection.Reason]++
		if len(rejections) == cap(rejections) {
			return flushRejections()
		}
		return nil
	}

	for _, input := range m.Inputs {
		kind := sourceKind(input.URL)
		phaseStarted := time.Now()
		assets, assetErr := streamingAssets(filepath.Join(dataDir, m.Catalog.File), input, m.Scopes)
		if assetErr != nil {
			return audit, assetErr
		}
		if got := assetDigest(assets); got != input.AssetsSHA256 {
			return audit, fmt.Errorf("%s asset-set checksum mismatch: got %s", kind, got)
		}
		remotes := make([]*rangeFile, 0, len(assets))
		versionHash := sha256.New()
		for _, asset := range assets {
			remote, openErr := openRange(ctx, asset)
			if openErr != nil {
				return audit, openErr
			}
			fmt.Fprintf(versionHash, "%s\t%d\t%s\n", asset, remote.size, remote.etag)
			remotes = append(remotes, remote)
		}
		if got := hex.EncodeToString(versionHash.Sum(nil)); got != input.AssetVersionsSHA256 {
			return audit, fmt.Errorf("%s asset-version checksum mismatch: got %s", kind, got)
		}
		for assetIndex := range assets {
			remote := remotes[assetIndex]
			file, openErr := parquet.OpenFile(remote, remote.size, parquet.SkipPageIndex(true), parquet.SkipBloomFilters(true))
			if openErr != nil {
				return audit, openErr
			}
			for groupIndex, group := range file.RowGroups() {
				if kind != "division" && !overlapsAnyStatsFormat(file.Metadata().RowGroups[groupIndex], scopeBounds(m.Scopes)) {
					continue
				}
				reader := parquet.NewGenericRowGroupReader[any](group)
				rows := make([]any, 128)
				for {
					n, readErr := reader.Read(rows)
					for _, value := range rows[:n] {
						row := value.(map[string]any)
						if kind != "division" && !overlapsAnyMap(row["bbox"], scopeBounds(m.Scopes)) {
							continue
						}
						feature, featureErr := parquetFeature(row, file.Schema())
						if featureErr != nil {
							reader.Close()
							return audit, featureErr
						}
						encoded, featureErr := json.Marshal(feature)
						if featureErr != nil {
							return audit, featureErr
						}
						audit.SourceRows[kind]++
						if featureErr = streamFeature(aux, kind, input.Release, encoded, m.Scopes, addRecord, addRejection); featureErr != nil {
							reader.Close()
							return audit, featureErr
						}
					}
					if readErr != nil {
						reader.Close()
						if readErr != io.EOF {
							return audit, readErr
						}
						break
					}
					if err = ctx.Err(); err != nil {
						reader.Close()
						return audit, err
					}
				}
			}
			audit.RemoteBytesRead += remote.fetched
			remote.releaseCache()
		}
		audit.PhaseSeconds["source_"+kind] = time.Since(phaseStarted).Seconds()
	}
	joinsStarted := time.Now()
	if err = aux.flush(ctx); err != nil {
		return audit, err
	}
	if err = emitDivisions(ctx, db, m.Streaming.BatchRows, sink, &audit); err != nil {
		return audit, err
	}
	if err = emitAddressLinks(ctx, db, m.Streaming.BatchRows, sink, &audit); err != nil {
		return audit, err
	}
	if err = flushRecords(); err != nil {
		return audit, err
	}
	if err = flushRejections(); err != nil {
		return audit, err
	}
	audit.PhaseSeconds["joins_and_hierarchy"] = time.Since(joinsStarted).Seconds()
	return audit, ctx.Err()
}

func streamFeature(aux *auxStager, kind, release string, raw json.RawMessage, scopes []Scope, addRecord func(Record) error, addRejection func(Rejection) error) error {
	if kind == "segment" {
		var feature transportationFeature
		feature.Raw = raw
		if err := json.Unmarshal(raw, &feature); err != nil {
			return err
		}
		if err := json.Unmarshal(feature.Properties, &feature.Props); err != nil {
			return err
		}
		key := "overture:segment:" + feature.Props.ID
		if feature.Type != "Feature" || feature.Geometry.Type != "LineString" || feature.Props.ID == "" || !validLineString(feature.Geometry.Coordinates) {
			return fmt.Errorf("invalid Overture transportation segment: %s", feature.Props.ID)
		}
		if feature.Props.Subtype != "road" {
			return addRejection(Rejection{SourceKey: key, Reason: "non_road_segment", Raw: raw})
		}
		if strings.TrimSpace(feature.Props.Names.Primary) == "" {
			return addRejection(Rejection{SourceKey: key, Reason: "missing_primary_name", Raw: raw})
		}
		location, ok := representativeCoordinateScopes(feature.Geometry.Coordinates, scopes)
		if !ok {
			return addRejection(Rejection{SourceKey: key, Reason: "outside_manifest_geometry", Raw: raw})
		}
		aliases, err := transportationAliases(feature.Props)
		if err != nil {
			return err
		}
		record := Record{Source: "overture:segment", SourceID: feature.Props.ID, Release: release, Kind: "street", Priority: 100,
			Attributes: map[string]json.RawMessage{"name": rawValue(feature.Props.Names.Primary), "location": rawValue(map[string]float64{"lat": location[1], "lng": location[0]}), "aliases": rawValue(aliases)},
			Paths:      map[string]string{"name": "/properties/names/primary", "location": "/geometry (spherical-length midpoint of centerline clipped to configured scope)", "aliases": "/properties/names/common + /properties/names/rules/*/value"}, Raw: raw,
			Attributions: []places.Attribution{{Provider: "© OpenStreetMap contributors, Overture Maps Foundation", URI: "https://docs.overturemaps.org/attribution/"}}}
		if !recordSearchable(record) {
			return addRejection(Rejection{SourceKey: key, Reason: "missing_search_text", Raw: raw})
		}
		return addRecord(record)
	}
	var feature overtureFeature
	feature.Raw = raw
	if err := json.Unmarshal(raw, &feature.feature); err != nil {
		return err
	}
	if err := json.Unmarshal(feature.Properties, &feature.Props); err != nil {
		return err
	}
	key := "overture:" + kind + ":" + feature.Props.ID
	if feature.Type != "Feature" || feature.Geometry.Type != "Point" || feature.Props.ID == "" || !validLocation(places.Location{Lat: feature.Geometry.Coordinates[1], Lng: feature.Geometry.Coordinates[0]}) {
		return fmt.Errorf("invalid Overture point: %s", feature.Props.ID)
	}
	candidate := insideScopes(feature.Geometry.Coordinates, scopes)
	inside := candidate && countryAllowed(feature.Props, kind) && regionAllowed(feature.Props, kind, scopes)
	if kind == "division" {
		rejection, rejectionErr := divisionSearchRejection(feature.Props)
		if rejectionErr != nil {
			return rejectionErr
		}
		return aux.addDivision(auxDivision{id: feature.Props.ID, parent: feature.Props.Parent, rejection: rejection, inside: inside, candidate: candidate, release: release, raw: string(raw)})
	}
	if !inside {
		return addRejection(Rejection{SourceKey: key, Reason: "outside_configured_scope", Raw: raw})
	}
	if kind == "address" && strings.TrimSpace(feature.Props.Number+" "+feature.Props.Street) == "" || kind == "place" && strings.TrimSpace(feature.Props.Names.Primary) == "" {
		return addRejection(Rejection{SourceKey: key, Reason: "missing_display_name", Raw: raw})
	}
	record, err := overtureRecord(feature, kind, release)
	if err != nil {
		return err
	}
	if !recordSearchable(record) {
		return addRejection(Rejection{SourceKey: key, Reason: "missing_search_text", Raw: raw})
	}
	if err = addRecord(record); err != nil {
		return err
	}
	if kind == "address" {
		err = aux.addAddress(auxLink{record.Key(), normalizeAddress(recordName(record)), postcodePrefix(feature.Props.Postcode), feature.Geometry.Coordinates[1], feature.Geometry.Coordinates[0]})
	} else if len(feature.Props.Addresses) > 0 {
		a := feature.Props.Addresses[0]
		err = aux.addBusiness(auxLink{record.Key(), normalizeAddress(a.Freeform), postcodePrefix(a.Postcode), feature.Geometry.Coordinates[1], feature.Geometry.Coordinates[0]})
	}
	return err
}

type auxDivision struct {
	id, parent, rejection string
	inside, candidate     bool
	release, raw          string
}

type auxLink struct {
	key, address, postcode string
	lat, lng               float64
}

type auxStager struct {
	ctx                   context.Context
	db                    *sql.DB
	batch                 int
	divisions             []auxDivision
	addresses, businesses []auxLink
}

func newAuxStager(ctx context.Context, db *sql.DB, batch int) *auxStager {
	return &auxStager{ctx: ctx, db: db, batch: batch, divisions: make([]auxDivision, 0, batch), addresses: make([]auxLink, 0, batch), businesses: make([]auxLink, 0, batch)}
}

func (s *auxStager) addDivision(row auxDivision) error {
	s.divisions = append(s.divisions, row)
	if len(s.divisions) == s.batch {
		return s.flushDivisions()
	}
	return nil
}

func (s *auxStager) addAddress(row auxLink) error {
	s.addresses = append(s.addresses, row)
	if len(s.addresses) == s.batch {
		return s.flushAddresses()
	}
	return nil
}

func (s *auxStager) addBusiness(row auxLink) error {
	s.businesses = append(s.businesses, row)
	if len(s.businesses) == s.batch {
		return s.flushBusinesses()
	}
	return nil
}

func (s *auxStager) flush(ctx context.Context) error {
	if err := s.flushDivisions(); err != nil {
		return err
	}
	if err := s.flushAddresses(); err != nil {
		return err
	}
	return s.flushBusinesses()
}

func (s *auxStager) flushDivisions() error {
	err := insertAuxBatch(s.ctx, s.db, "INSERT INTO divisions VALUES (?,?,?,?,?,?,?)", len(s.divisions), func(stmt *sql.Stmt, i int) error {
		r := s.divisions[i]
		_, err := stmt.ExecContext(s.ctx, r.id, r.parent, r.inside, r.candidate, r.rejection, r.release, r.raw)
		return err
	})
	s.divisions = s.divisions[:0]
	return err
}

func (s *auxStager) flushAddresses() error {
	err := insertAuxBatch(s.ctx, s.db, "INSERT INTO link_addresses VALUES (?,?,?,?,?)", len(s.addresses), func(stmt *sql.Stmt, i int) error {
		r := s.addresses[i]
		_, err := stmt.ExecContext(s.ctx, r.key, r.address, r.postcode, r.lat, r.lng)
		return err
	})
	s.addresses = s.addresses[:0]
	return err
}

func (s *auxStager) flushBusinesses() error {
	err := insertAuxBatch(s.ctx, s.db, "INSERT INTO link_businesses VALUES (?,?,?,?,?)", len(s.businesses), func(stmt *sql.Stmt, i int) error {
		r := s.businesses[i]
		_, err := stmt.ExecContext(s.ctx, r.key, r.address, r.postcode, r.lat, r.lng)
		return err
	})
	s.businesses = s.businesses[:0]
	return err
}

func insertAuxBatch(ctx context.Context, db *sql.DB, query string, count int, add func(*sql.Stmt, int) error) error {
	if count == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return err
	}
	for i := 0; i < count; i++ {
		if err = add(stmt, i); err != nil {
			stmt.Close()
			return err
		}
	}
	if err = stmt.Close(); err != nil {
		return err
	}
	return tx.Commit()
}

func countryAllowed(props overtureProperties, kind string) bool {
	if kind == "address" || kind == "division" {
		return props.Country == "US"
	}
	return len(props.Addresses) == 0 || props.Addresses[0].Country == "" || props.Addresses[0].Country == "US"
}

func regionAllowed(props overtureProperties, kind string, scopes []Scope) bool {
	allowed := map[string]bool{}
	for _, scope := range scopes {
		for _, region := range scope.Regions {
			allowed[region] = true
		}
	}
	if len(allowed) == 0 || kind == "division" {
		return true
	}
	region := ""
	if kind == "address" && len(props.AddressLevels) > 0 {
		region = props.AddressLevels[0].Value
	} else if kind == "place" && len(props.Addresses) > 0 {
		region = props.Addresses[0].Region
	}
	return allowed[region]
}

func insideScopes(point [2]float64, scopes []Scope) bool {
	for _, scope := range scopes {
		if inside(point, scope.BBox) {
			return true
		}
	}
	return false
}

func divisionSearchRejection(props overtureProperties) (string, error) {
	if strings.TrimSpace(props.Names.Primary) == "" {
		return "missing_primary_name", nil
	}
	if places.Normalize(props.Names.Primary) != "" {
		return "", nil
	}
	if len(props.Names.Common) > 0 && string(props.Names.Common) != "null" {
		var pairs [][]string
		if err := json.Unmarshal(props.Names.Common, &pairs); err != nil {
			return "", err
		}
		for _, pair := range pairs {
			if len(pair) != 2 {
				return "", fmt.Errorf("invalid name pair")
			}
			if places.Normalize(pair[1]) != "" {
				return "", nil
			}
		}
	}
	return "missing_search_text", nil
}

func recordSearchable(record Record) bool {
	for _, attribute := range []string{"name", "address"} {
		var value string
		if json.Unmarshal(record.Attributes[attribute], &value) == nil && places.Normalize(value) != "" {
			return true
		}
	}
	var aliases []string
	if json.Unmarshal(record.Attributes["aliases"], &aliases) == nil {
		for _, alias := range aliases {
			if places.Normalize(alias) != "" {
				return true
			}
		}
	}
	return false
}

func representativeCoordinateScopes(points [][2]float64, scopes []Scope) ([2]float64, bool) {
	// Configured scope boxes are required to be disjoint, so retained portions
	// cannot be double-counted. Preserve source-edge order across the union.
	type portion struct {
		start, end [2]float64
		length     float64
	}
	var portions []portion
	total := 0.0
	for i := 1; i < len(points); i++ {
		for _, scope := range scopes {
			start, end, ok := clipLine(points[i-1], points[i], scope.BBox)
			if !ok {
				continue
			}
			length := distance(start, end)
			portions = append(portions, portion{start, end, length})
			total += length
		}
	}
	if len(portions) == 0 {
		return [2]float64{}, false
	}
	if total == 0 {
		return portions[0].start, true
	}
	target, covered := total/2, 0.0
	for _, portion := range portions {
		if covered+portion.length >= target {
			fraction := (target - covered) / portion.length
			return [2]float64{portion.start[0] + fraction*(portion.end[0]-portion.start[0]), portion.start[1] + fraction*(portion.end[1]-portion.start[1])}, true
		}
		covered += portion.length
	}
	return portions[len(portions)-1].end, true
}

func emitDivisions(ctx context.Context, db *sql.DB, batch int, sink PreparedSink, audit *StreamingAudit) error {
	var duplicates int
	if err := db.QueryRowContext(ctx, "SELECT count(*)-count(DISTINCT id) FROM divisions").Scan(&duplicates); err != nil || duplicates != 0 {
		return fmt.Errorf("duplicate Overture divisions: %d: %w", duplicates, err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE selected_divisions AS
WITH RECURSIVE selected(id) AS (
  SELECT id FROM divisions WHERE inside AND rejection=''
  UNION
  SELECT d.parent FROM divisions d JOIN selected s ON d.id=s.id JOIN divisions p ON p.id=d.parent WHERE d.parent<>'' AND p.rejection=''
)
SELECT id FROM selected ORDER BY id`); err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT d.id,d.release,d.parent,d.rejection,s.id IS NOT NULL,ps.id IS NOT NULL,d.raw
	FROM divisions d LEFT JOIN selected_divisions s USING(id)
	LEFT JOIN selected_divisions ps ON ps.id=d.parent
	WHERE s.id IS NOT NULL OR d.candidate ORDER BY d.id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	records := make([]Record, 0, batch)
	rejections := make([]Rejection, 0, batch)
	relationships := make([]Relationship, 0, batch)
	flush := func() error {
		audit.MaxSubmittedBatch = max(audit.MaxSubmittedBatch, len(records), len(rejections), len(relationships))
		if err := sink.WriteRecords(ctx, records); err != nil {
			return err
		}
		if err := sink.WriteRelationships(ctx, relationships); err != nil {
			return err
		}
		if err := sink.WriteRejections(ctx, rejections); err != nil {
			return err
		}
		records, relationships, rejections = records[:0], relationships[:0], rejections[:0]
		return nil
	}
	for rows.Next() {
		var id, release, parent, rejection, raw string
		var selected, parentSelected bool
		if err = rows.Scan(&id, &release, &parent, &rejection, &selected, &parentSelected, &raw); err != nil {
			return err
		}
		if selected {
			var feature overtureFeature
			feature.Raw = json.RawMessage(raw)
			if err = json.Unmarshal([]byte(raw), &feature.feature); err != nil {
				return err
			}
			if err = json.Unmarshal(feature.Properties, &feature.Props); err != nil {
				return err
			}
			record, recordErr := overtureRecord(feature, "division", release)
			if recordErr != nil {
				return recordErr
			}
			records = append(records, record)
			audit.ImportedCounts[record.Kind]++
			if parent != "" {
				if parentSelected {
					relationships = append(relationships, Relationship{From: record.Key(), To: "overture:division:" + parent, Kind: "parent_area", Evidence: "Overture parent_division_id"})
					audit.Relationships["parent_area"]++
				}
			}
		} else {
			reason := "outside_manifest_hierarchy"
			if rejection != "" {
				reason = rejection
			}
			rejections = append(rejections, Rejection{SourceKey: "overture:division:" + id, Reason: reason, Raw: json.RawMessage(raw)})
			audit.RejectedCounts[reason]++
		}
		if len(records) == cap(records) || len(rejections) == cap(rejections) || len(relationships) == cap(relationships) {
			if err = flush(); err != nil {
				return err
			}
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	return flush()
}

func emitAddressLinks(ctx context.Context, db *sql.DB, batch int, sink PreparedSink, audit *StreamingAudit) error {
	rows, err := db.QueryContext(ctx, `WITH candidates AS (
 SELECT b.source_key AS business_key,a.source_key AS address_key,
  6371008.8*2*asin(least(1,sqrt(pow(sin(radians(a.lat-b.lat)/2),2)+cos(radians(b.lat))*cos(radians(a.lat))*pow(sin(radians(a.lng-b.lng)/2),2)))) AS metres
 FROM link_businesses b JOIN link_addresses a USING(address_key,postcode)
), qualifying AS (SELECT * FROM candidates WHERE metres<=50), unique_links AS (
 SELECT business_key,min(address_key) AS address_key,count(*) AS matches FROM qualifying GROUP BY business_key
)
SELECT business_key,address_key,matches FROM unique_links ORDER BY business_key`)
	if err != nil {
		return err
	}
	defer rows.Close()
	batchRows := make([]Relationship, 0, batch)
	for rows.Next() {
		var from, to string
		var matches int
		if err = rows.Scan(&from, &to, &matches); err != nil {
			return err
		}
		if matches == 1 {
			batchRows = append(batchRows, Relationship{From: from, To: to, Kind: "address", Evidence: "Exact normalized number/street + postcode; unique within 50 metres"})
			audit.Relationships["address"]++
		} else {
			audit.Relationships["ambiguous_address_candidates"]++
		}
		if len(batchRows) == cap(batchRows) {
			audit.MaxSubmittedBatch = max(audit.MaxSubmittedBatch, len(batchRows))
			if err = sink.WriteRelationships(ctx, batchRows); err != nil {
				return err
			}
			batchRows = batchRows[:0]
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	audit.MaxSubmittedBatch = max(audit.MaxSubmittedBatch, len(batchRows))
	return sink.WriteRelationships(ctx, batchRows)
}

func overlapsAnyMap(value any, bounds [][4]float64) bool {
	for _, bound := range bounds {
		if overlapsMap(value, bound) {
			return true
		}
	}
	return false
}
