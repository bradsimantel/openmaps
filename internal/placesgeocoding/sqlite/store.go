// Package sqlite implements the opt-in SQLite FTS5/RTree Places Autocomplete
// experiment. It never participates in production lookup selection.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"openmaps/internal/importer"
	"openmaps/internal/places"
)

const candidateLimit = 50

type DetailsStore interface {
	Details(context.Context, string) (places.Entity, error)
}
type Store struct {
	shards  []*sql.DB
	files   []string
	details DetailsStore
}

func Open(path string, details DetailsStore) (store *Store, err error) {
	if details == nil {
		return nil, fmt.Errorf("Details store is required")
	}
	if _, err := os.Stat(filepath.Join(path, IncompleteName)); err == nil {
		return nil, fmt.Errorf("SQLite generation is incomplete: %s", path)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(path, ManifestName))
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}
	if manifest.Schema != SchemaVersion || manifest.CoordinateOrder != "longitude,latitude" || len(manifest.Shards) == 0 {
		return nil, fmt.Errorf("unsupported SQLite experiment manifest")
	}
	s := &Store{details: details}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	for _, f := range manifest.Shards {
		if err = importer.Verify(filepath.Join(path, f.Name), f.SHA256); err != nil {
			return nil, err
		}
		u := url.URL{Scheme: "file", Path: filepath.Join(path, f.Name)}
		q := u.Query()
		q.Set("mode", "ro")
		q.Set("immutable", "1")
		u.RawQuery = q.Encode()
		db, e := sql.Open("sqlite", u.String())
		if e != nil {
			return nil, e
		}
		db.SetMaxOpenConns(4)
		db.SetMaxIdleConns(4)
		for _, p := range []string{"query_only=ON", "foreign_keys=ON", "temp_store=MEMORY", "cache_size=-65536", "mmap_size=0"} {
			if _, e = db.Exec("PRAGMA " + p); e != nil {
				db.Close()
				return nil, e
			}
		}
		var version string
		if e = db.QueryRow("SELECT value FROM metadata WHERE key='schema_version'").Scan(&version); e != nil || version != "1" {
			db.Close()
			return nil, fmt.Errorf("SQLite shard schema: %v", e)
		}
		s.shards = append(s.shards, db)
		s.files = append(s.files, f.Name)
	}
	return s, nil
}

// Inventory returns read-only storage evidence for each finalized shard. The
// dbstat virtual table reports bytes by SQLite b-tree and virtual-table shadow
// object, which keeps FTS and RTree storage distinguishable in the bakeoff.
type ShardInventory struct {
	Name           string           `json:"name"`
	PageSize       int64            `json:"page_size"`
	PageCount      int64            `json:"page_count"`
	FreelistCount  int64            `json:"freelist_count"`
	LogicalBytes   int64            `json:"logical_bytes"`
	ObjectBytes    map[string]int64 `json:"object_bytes"`
	Indexes        []string         `json:"indexes"`
	JournalMode    string           `json:"journal_mode"`
	QueryOnly      int64            `json:"query_only"`
	TemporaryStore int64            `json:"temp_store"`
	CacheSize      int64            `json:"cache_size"`
	MemoryMapBytes int64            `json:"mmap_size"`
}

func (s *Store) Inventory(ctx context.Context) ([]ShardInventory, error) {
	out := make([]ShardInventory, 0, len(s.shards))
	for i, db := range s.shards {
		item := ShardInventory{Name: s.files[i], ObjectBytes: map[string]int64{}}
		if err := db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&item.PageSize); err != nil {
			return nil, err
		}
		if err := db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&item.PageCount); err != nil {
			return nil, err
		}
		if err := db.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&item.FreelistCount); err != nil {
			return nil, err
		}
		item.LogicalBytes = item.PageSize * item.PageCount
		if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&item.JournalMode); err != nil {
			return nil, err
		}
		for pragma, destination := range map[string]*int64{
			"query_only": &item.QueryOnly, "temp_store": &item.TemporaryStore,
			"cache_size": &item.CacheSize, "mmap_size": &item.MemoryMapBytes,
		} {
			if err := db.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(destination); err != nil {
				return nil, err
			}
		}
		rows, err := db.QueryContext(ctx, "SELECT name,sum(pgsize) FROM dbstat GROUP BY name ORDER BY name")
		if err != nil {
			return nil, fmt.Errorf("dbstat %s: %w", item.Name, err)
		}
		for rows.Next() {
			var name string
			var size int64
			if err = rows.Scan(&name, &size); err != nil {
				rows.Close()
				return nil, err
			}
			item.ObjectBytes[name] = size
		}
		if err = rows.Close(); err != nil {
			return nil, err
		}
		rows, err = db.QueryContext(ctx, "SELECT name FROM sqlite_schema WHERE type='index' ORDER BY name")
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var name string
			if err = rows.Scan(&name); err != nil {
				rows.Close()
				return nil, err
			}
			item.Indexes = append(item.Indexes, name)
		}
		if err = rows.Close(); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}
func (s *Store) Close() error {
	var first error
	for _, db := range s.shards {
		if e := db.Close(); e != nil && first == nil {
			first = e
		}
	}
	return first
}
func (s *Store) Details(ctx context.Context, id string) (places.Entity, error) {
	return s.details.Details(ctx, id)
}
func (s *Store) Autocomplete(ctx context.Context, input string) ([]places.Entity, error) {
	return s.AutocompleteWithBias(ctx, input, nil)
}

type candidate struct {
	rowid                                                                         int64
	id, kind, subtype, name, normalizedName, address                              string
	locality, region, regionCode, country, postal                                 string
	lat, lng                                                                      float64
	areaProminence, settlementTier, destinationClass, specificity, confidenceTier int
	areaOverride                                                                  bool
	bm25                                                                          float64
	textClass                                                                     int
	local                                                                         bool
}

func (c candidate) entity() places.Entity {
	return places.Entity{ID: c.id, Kind: c.kind, Subtype: c.subtype, Name: c.name, Address: c.address, Location: places.Location{Lat: c.lat, Lng: c.lng}}
}

func (s *Store) AutocompleteWithBias(ctx context.Context, input string, bias *places.Viewport) ([]places.Entity, error) {
	if parsed, ok := places.ParseAutocompleteContext(input); ok {
		return s.structured(ctx, parsed, bias)
	}
	normalized := places.Normalize(input)
	if normalized == "" {
		return []places.Entity{}, nil
	}
	candidates, err := s.retrieve(ctx, normalized, bias)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 && containsLetter(normalized) && utf8.RuneCountInString(normalized) >= 4 {
		fallback, e := s.fuzzy(ctx, normalized, bias)
		if e != nil {
			return nil, e
		}
		candidates = mergeCandidates(candidates, fallback)
	}
	return s.materialize(ctx, rankCandidates(normalized, candidates, bias))
}

func (s *Store) retrieve(ctx context.Context, normalized string, bias *places.Viewport) ([]candidate, error) {
	type result struct {
		c []candidate
		e error
	}
	ch := make(chan result, len(s.shards))
	for _, db := range s.shards {
		go func(db *sql.DB) { c, e := retrieveShard(ctx, db, normalized, bias); ch <- result{c, e} }(db)
	}
	out := []candidate{}
	for range s.shards {
		r := <-ch
		if r.e != nil {
			return nil, r.e
		}
		out = mergeCandidates(out, r.c)
	}
	return out, nil
}

func retrieveShard(ctx context.Context, db *sql.DB, normalized string, bias *places.Viewport) ([]candidate, error) {
	out := []candidate{}
	if !strings.Contains(normalized, " ") && utf8.RuneCountInString(normalized) <= 2 && asciiPrefix(normalized) {
		rows, e := queryCandidates(ctx, db, `SELECT `+candidateColumns("e")+`,0.0 FROM short_prefix_head h JOIN entities e ON e.rowid=h.entity_rowid WHERE h.prefix=? AND e.closed=0 ORDER BY h.rank LIMIT 64`, normalized)
		if e != nil {
			return nil, e
		}
		for i := range rows {
			rows[i].textClass = classifyText(normalized, rows[i].normalizedName)
		}
		return rows, nil
	}
	if asciiDigits(normalized) {
		rows, err := queryCandidates(ctx, db, `SELECT `+candidateColumns("e")+`,0.0 FROM entities e INDEXED BY entities_exact_name WHERE e.normalized_name=? AND e.kind='area' AND e.closed=0 ORDER BY e.area_prominence DESC,e.settlement_tier DESC,e.id LIMIT 32`, normalized)
		if err != nil {
			return nil, err
		}
		for i := range rows {
			rows[i].textClass = 0
		}
		return rows, nil
	}
	for _, prefix := range primaryPrefixes(normalized) {
		for _, query := range []string{
			`SELECT ` + candidateColumns("e") + `,0.0 FROM entities e INDEXED BY entities_exact_name WHERE e.normalized_name=? AND e.kind='area' AND e.closed=0 ORDER BY CASE WHEN e.subtype='locality' AND e.settlement_tier=4 THEN 0 WHEN e.subtype='region' THEN 1 WHEN e.subtype='locality' THEN 2 ELSE 3 END,e.area_prominence DESC,e.id LIMIT 32`,
			`SELECT ` + candidateColumns("e") + `,0.0 FROM entities e INDEXED BY entities_exact_name WHERE e.normalized_name=? AND e.kind='business' AND e.closed=0 ORDER BY e.area_override DESC,e.confidence_tier DESC,e.specificity DESC,e.destination_class DESC,e.id LIMIT 32`,
		} {
			rows, err := queryCandidates(ctx, db, query, prefix)
			if err != nil {
				return nil, err
			}
			for i := range rows {
				rows[i].textClass = classifyText(normalized, rows[i].normalizedName)
			}
			out = mergeCandidates(out, rows)
		}
		upper := prefixUpper(prefix)
		for _, kind := range []string{"area", "street", "business", "address"} {
			rows, err := queryCandidates(ctx, db, `SELECT `+candidateColumns("e")+`,0.0 FROM entities e INDEXED BY entities_exact_name WHERE e.normalized_name>=? AND e.normalized_name<? AND e.kind=? AND e.closed=0 LIMIT 32`, prefix, upper, kind)
			if err != nil {
				return nil, err
			}
			for i := range rows {
				rows[i].textClass = classifyText(normalized, rows[i].normalizedName)
			}
			out = mergeCandidates(out, rows)
		}
	}
	if bias != nil {
		local, err := queryLocalPrimary(ctx, db, normalized, *bias)
		if err != nil {
			return nil, err
		}
		for i := range local {
			local[i].textClass = classifyText(normalized, local[i].normalizedName)
			local[i].local = true
		}
		out = mergeCandidates(out, local)
	}
	if len(out) > 0 {
		return out, nil
	}
	expression := ftsExpression(normalized)
	strict, e := queryCandidates(ctx, db, `SELECT `+candidateColumns("e")+`,bm25(entity_fts,10.0,5.0,1.0,2.0) FROM entity_fts JOIN entities e ON e.rowid=entity_fts.rowid WHERE entity_fts MATCH ? AND e.closed=0 ORDER BY bm25(entity_fts,10.0,5.0,1.0,2.0),e.id LIMIT 64`, expression)
	if e != nil {
		return nil, e
	}
	for i := range strict {
		strict[i].textClass = classifyText(normalized, strict[i].normalizedName)
	}
	out = mergeCandidates(out, strict)
	if bias != nil {
		local, e := queryLocal(ctx, db, expression, normalized, *bias)
		if e != nil {
			return nil, e
		}
		for i := range local {
			local[i].textClass = classifyText(normalized, local[i].normalizedName)
			local[i].local = true
		}
		out = mergeCandidates(out, local)
	}
	return out, nil
}

func queryLocalPrimary(ctx context.Context, db *sql.DB, normalized string, v places.Viewport) ([]candidate, error) {
	query := `SELECT ` + candidateColumns("e") + `,0.0 FROM entities e INDEXED BY entities_exact_name CROSS JOIN entity_rtree r ON r.rowid=e.rowid WHERE e.normalized_name>=? AND e.normalized_name<? AND e.closed=0 AND r.max_lat>=? AND r.min_lat<=? AND r.max_lng>=? AND r.min_lng<=? LIMIT 64`
	out := []candidate{}
	for _, prefix := range primaryPrefixes(normalized) {
		upper := prefixUpper(prefix)
		if v.West <= v.East {
			rows, err := queryCandidates(ctx, db, query, prefix, upper, v.South, v.North, v.West, v.East)
			if err != nil {
				return nil, err
			}
			out = mergeCandidates(out, rows)
			continue
		}
		left, err := queryCandidates(ctx, db, query, prefix, upper, v.South, v.North, v.West, 180.0)
		if err != nil {
			return nil, err
		}
		right, err := queryCandidates(ctx, db, query, prefix, upper, v.South, v.North, -180.0, v.East)
		if err != nil {
			return nil, err
		}
		out = mergeCandidates(out, mergeCandidates(left, right))
	}
	return out, nil
}
func queryLocal(ctx context.Context, db *sql.DB, expression, normalized string, v places.Viewport) ([]candidate, error) {
	query := `SELECT ` + candidateColumns("e") + `,bm25(entity_fts,10.0,5.0,1.0,2.0) FROM entity_fts JOIN entities e ON e.rowid=entity_fts.rowid JOIN entity_rtree r ON r.rowid=e.rowid WHERE entity_fts MATCH ? AND e.closed=0 AND r.max_lat>=? AND r.min_lat<=? AND r.max_lng>=? AND r.min_lng<=? ORDER BY CASE WHEN e.normalized_name=? THEN 0 WHEN e.normalized_name LIKE ? THEN 1 ELSE 2 END,bm25(entity_fts,10.0,5.0,1.0,2.0),e.id LIMIT 64`
	if v.West <= v.East {
		return queryCandidates(ctx, db, query, expression, v.South, v.North, v.West, v.East, normalized, normalized+"%")
	}
	left, e := queryCandidates(ctx, db, query, expression, v.South, v.North, v.West, 180.0, normalized, normalized+"%")
	if e != nil {
		return nil, e
	}
	right, e := queryCandidates(ctx, db, query, expression, v.South, v.North, -180.0, v.East, normalized, normalized+"%")
	return mergeCandidates(left, right), e
}
func candidateColumns(a string) string {
	return strings.Join([]string{a + ".rowid", a + ".id", a + ".kind", a + ".subtype", a + ".name", a + ".normalized_name", a + ".formatted_address", a + ".locality", a + ".region", a + ".region_code", a + ".country", a + ".postal_code", a + ".lat", a + ".lng", a + ".area_prominence", a + ".settlement_tier", a + ".destination_class", a + ".specificity", a + ".confidence_tier", a + ".area_override"}, ",")
}
func queryCandidates(ctx context.Context, db *sql.DB, query string, args ...any) ([]candidate, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.rowid, &c.id, &c.kind, &c.subtype, &c.name, &c.normalizedName, &c.address, &c.locality, &c.region, &c.regionCode, &c.country, &c.postal, &c.lat, &c.lng, &c.areaProminence, &c.settlementTier, &c.destinationClass, &c.specificity, &c.confidenceTier, &c.areaOverride, &c.bm25); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func ftsExpression(normalized string) string {
	tokens := strings.Fields(normalized)
	parts := make([]string, len(tokens))
	for i, t := range tokens {
		t = strings.ReplaceAll(t, `"`, `""`)
		parts[i] = `"` + t + `"`
		if i == len(tokens)-1 {
			parts[i] += "*"
		}
	}
	return strings.Join(parts, " AND ")
}
func classifyText(query, name string) int {
	trimmed := strings.TrimPrefix(name, "the ")
	if name == query || trimmed == query {
		return 0
	}
	if strings.HasPrefix(name, query) || strings.HasPrefix(trimmed, query) {
		return 1
	}
	return 2
}

func (s *Store) fuzzy(ctx context.Context, normalized string, bias *places.Viewport) ([]candidate, error) {
	tokens := strings.Fields(normalized)
	if len(tokens) == 0 || len(tokens) > 4 {
		return nil, nil
	}
	alternatives := make([][]string, len(tokens))
	for i, t := range tokens {
		alternatives[i] = []string{t}
		if utf8.RuneCountInString(t) < 4 {
			continue
		}
		terms, err := s.nearTerms(ctx, t)
		if err != nil {
			return nil, err
		}
		alternatives[i] = append(alternatives[i], terms...)
	}
	groups := make([]string, len(alternatives))
	for i, terms := range alternatives {
		quoted := make([]string, len(terms))
		for j, t := range terms {
			quoted[j] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
			if i == len(groups)-1 {
				quoted[j] += "*"
			}
		}
		groups[i] = "(" + strings.Join(quoted, " OR ") + ")"
	}
	expr := strings.Join(groups, " AND ")
	type result struct {
		c []candidate
		e error
	}
	ch := make(chan result, len(s.shards))
	for _, db := range s.shards {
		go func(db *sql.DB) {
			c, e := queryCandidates(ctx, db, `SELECT `+candidateColumns("e")+`,bm25(entity_fts,10.0,5.0,1.0,2.0) FROM entity_fts JOIN entities e ON e.rowid=entity_fts.rowid WHERE entity_fts MATCH ? AND e.closed=0 ORDER BY bm25(entity_fts,10.0,5.0,1.0,2.0),e.id LIMIT 32`, expr)
			ch <- result{c, e}
		}(db)
	}
	out := []candidate{}
	for range s.shards {
		r := <-ch
		if r.e != nil {
			return nil, r.e
		}
		for i := range r.c {
			r.c[i].textClass = 3
		}
		out = mergeCandidates(out, r.c)
	}
	return out, nil
}
func (s *Store) nearTerms(ctx context.Context, token string) ([]string, error) {
	prefix := string([]rune(token)[:2])
	upper := prefixUpper(prefix)
	set := map[string]bool{}
	var mu sync.Mutex
	var first error
	var wg sync.WaitGroup
	for _, db := range s.shards {
		wg.Add(1)
		go func(db *sql.DB) {
			defer wg.Done()
			rows, err := db.QueryContext(ctx, "SELECT term FROM entity_terms WHERE term>=? AND term<? AND length(term) BETWEEN ? AND ? ORDER BY doc DESC LIMIT 256", prefix, upper, max(1, utf8.RuneCountInString(token)-2), utf8.RuneCountInString(token)+2)
			if err != nil {
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
				return
			}
			defer rows.Close()
			for rows.Next() {
				var term string
				if rows.Scan(&term) == nil && boundedDistance(token, term, 2) <= 2 {
					mu.Lock()
					set[term] = true
					mu.Unlock()
				}
			}
		}(db)
	}
	wg.Wait()
	if first != nil {
		return nil, first
	}
	terms := make([]string, 0, len(set))
	for x := range set {
		if x != token {
			terms = append(terms, x)
		}
	}
	sort.Slice(terms, func(i, j int) bool {
		di, dj := boundedDistance(token, terms[i], 2), boundedDistance(token, terms[j], 2)
		if di != dj {
			return di < dj
		}
		return terms[i] < terms[j]
	})
	if len(terms) > 8 {
		terms = terms[:8]
	}
	return terms, nil
}
func prefixUpper(s string) string { r := []rune(s); r[len(r)-1]++; return string(r) }
func primaryPrefixes(normalized string) []string {
	if strings.HasPrefix(normalized, "the ") {
		return []string{normalized}
	}
	return []string{normalized, "the " + normalized}
}
func asciiDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
func containsLetter(s string) bool {
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}
func boundedDistance(a, b string, limit int) int {
	ar, br := []rune(a), []rune(b)
	if abs(len(ar)-len(br)) > limit {
		return limit + 1
	}
	prev := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, x := range ar {
		cur := make([]int, len(br)+1)
		cur[0] = i + 1
		rowMin := cur[0]
		for j, y := range br {
			cost := 0
			if x != y {
				cost = 1
			}
			cur[j+1] = min(cur[j]+1, prev[j+1]+1, prev[j]+cost)
			rowMin = min(rowMin, cur[j+1])
		}
		if rowMin > limit {
			return limit + 1
		}
		prev = cur
	}
	return prev[len(br)]
}
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (s *Store) structured(ctx context.Context, q places.AutocompleteContext, bias *places.Viewport) ([]places.Entity, error) {
	regions, err := s.retrieve(ctx, q.Region, nil)
	if err != nil {
		return nil, err
	}
	regions = filter(regions, func(c candidate) bool {
		return c.kind == "area" && c.subtype == "region" && c.normalizedName == q.Region
	})
	if len(regions) == 0 {
		return []places.Entity{}, nil
	}
	sort.Slice(regions, func(i, j int) bool { return betterCandidate(q.Region, regions[i], regions[j], nil) })
	regionAnchor := places.Location{Lat: regions[0].lat, Lng: regions[0].lng}
	locality := q.Name
	if q.Locality != "" {
		locality = q.Locality
	}
	localities, err := s.retrieve(ctx, locality, nil)
	if err != nil {
		return nil, err
	}
	localities = filter(localities, func(c candidate) bool {
		return c.kind == "area" && c.normalizedName == locality && (c.subtype == "locality" || c.subtype == "county" || c.subtype == "macrocounty")
	})
	if len(localities) == 0 {
		return []places.Entity{}, nil
	}
	sort.Slice(localities, func(i, j int) bool {
		di := places.DistanceMeters(regionAnchor, places.Location{Lat: localities[i].lat, Lng: localities[i].lng})
		dj := places.DistanceMeters(regionAnchor, places.Location{Lat: localities[j].lat, Lng: localities[j].lng})
		if di != dj {
			return di < dj
		}
		return betterCandidate(locality, localities[i], localities[j], nil)
	})
	if q.Locality == "" {
		return s.materialize(ctx, localities)
	}
	anchor := places.Location{Lat: localities[0].lat, Lng: localities[0].lng}
	streetBias := &places.Viewport{South: anchor.Lat - 1.0, North: anchor.Lat + 1.0, West: anchor.Lng - 1.5, East: anchor.Lng + 1.5}
	streets, err := s.retrieve(ctx, q.Name, streetBias)
	if err != nil {
		return nil, err
	}
	streets = filter(streets, func(c candidate) bool {
		return c.kind == "street" && c.normalizedName == q.Name && places.DistanceMeters(anchor, places.Location{Lat: c.lat, Lng: c.lng}) <= 100000
	})
	sort.Slice(streets, func(i, j int) bool {
		di := places.DistanceMeters(anchor, places.Location{Lat: streets[i].lat, Lng: streets[i].lng})
		dj := places.DistanceMeters(anchor, places.Location{Lat: streets[j].lat, Lng: streets[j].lng})
		if di != dj {
			return di < dj
		}
		return streets[i].id < streets[j].id
	})
	if len(streets) > 1 {
		streets = streets[:1]
	}
	return s.materialize(ctx, streets)
}
func filter(in []candidate, keep func(candidate) bool) []candidate {
	out := in[:0]
	for _, x := range in {
		if keep(x) {
			out = append(out, x)
		}
	}
	return out
}
func mergeCandidates(a, b []candidate) []candidate {
	seen := map[string]bool{}
	out := make([]candidate, 0, len(a)+len(b))
	for _, g := range [][]candidate{a, b} {
		for _, x := range g {
			if !seen[x.id] {
				seen[x.id] = true
				out = append(out, x)
			}
		}
	}
	return out
}
func rankCandidates(query string, in []candidate, bias *places.Viewport) []candidate {
	sort.SliceStable(in, func(i, j int) bool { return betterCandidate(query, in[i], in[j], bias) })
	out := make([]candidate, 0, 5)
	streets := map[string]bool{}
	for _, c := range in {
		if c.kind == "street" {
			if streets[c.normalizedName] {
				continue
			}
			streets[c.normalizedName] = true
		}
		out = append(out, c)
		if len(out) == 5 {
			break
		}
	}
	return out
}
func betterCandidate(query string, a, b candidate, bias *places.Viewport) bool {
	if a.textClass != b.textClass {
		return a.textClass < b.textClass
	}
	street := looksLikeStreet(query)
	if street && (a.kind == "street") != (b.kind == "street") {
		return a.kind == "street"
	}
	if a.textClass == 0 {
		sa, sb := strength(a), strength(b)
		if sa != sb {
			return sa > sb
		}
	}
	if bias != nil {
		center := bias.Center()
		ai, bi := bias.Contains(places.Location{Lat: a.lat, Lng: a.lng}), bias.Contains(places.Location{Lat: b.lat, Lng: b.lng})
		if ai != bi {
			return ai
		}
		da := places.DistanceMeters(center, places.Location{Lat: a.lat, Lng: a.lng})
		db := places.DistanceMeters(center, places.Location{Lat: b.lat, Lng: b.lng})
		if da != db {
			return da < db
		}
	}
	ka, kb := kindRank(a.kind), kindRank(b.kind)
	if ka != kb {
		return ka < kb
	}
	if a.bm25 != b.bm25 {
		return a.bm25 < b.bm25
	}
	return a.id < b.id
}
func strength(c candidate) int {
	if c.kind == "area" {
		base := 100000 - c.areaTypeRank()*10000
		return base + c.areaProminence
	}
	if c.kind == "business" {
		base := c.confidenceTier*10000 + c.specificity*10 + c.destinationClass
		if c.areaOverride {
			base += 110000
		}
		return base
	}
	if c.kind == "street" {
		return 5000
	}
	return 0
}
func (c candidate) areaTypeRank() int {
	if c.subtype == "locality" && c.settlementTier == int(places.SettlementCity) {
		return 0
	}
	switch c.subtype {
	case "region":
		return 1
	case "locality":
		return 2
	case "county", "macrocounty":
		return 3
	case "macrohood":
		return 4
	case "neighborhood", "microhood":
		return 5
	}
	return 6
}
func kindRank(kind string) int {
	switch kind {
	case "area":
		return 0
	case "street":
		return 1
	case "business":
		return 2
	}
	return 3
}
func looksLikeStreet(q string) bool {
	f := strings.Fields(q)
	if len(f) == 0 {
		return false
	}
	switch f[len(f)-1] {
	case "street", "st", "avenue", "ave", "boulevard", "blvd", "road", "rd", "highway", "hwy", "lane", "ln", "drive", "dr", "way", "route", "parkway", "trail", "terrace", "court", "circle", "broadway":
		return true
	}
	return false
}
func (s *Store) materialize(ctx context.Context, candidates []candidate) ([]places.Entity, error) {
	if len(candidates) > 5 {
		candidates = candidates[:5]
	}
	out := make([]places.Entity, 0, len(candidates))
	for _, c := range candidates {
		e, err := s.details.Details(ctx, c.id)
		if err != nil {
			return nil, fmt.Errorf("candidate %s details: %w", c.id, err)
		}
		out = append(out, e)
	}
	return out, nil
}

// QueryPlans returns representative plans for the report and schema tests.
func (s *Store) QueryPlans(ctx context.Context) (map[string][]string, error) {
	if len(s.shards) == 0 {
		return nil, fmt.Errorf("no shards")
	}
	db := s.shards[0]
	queries := map[string]struct {
		q string
		a []any
	}{"exact": {`SELECT rowid FROM entities INDEXED BY entities_exact_name WHERE normalized_name=? AND closed=0 LIMIT 50`, []any{"main street"}}, "prefix": {`SELECT e.rowid FROM entity_fts JOIN entities e ON e.rowid=entity_fts.rowid WHERE entity_fts MATCH ? AND e.closed=0 LIMIT 50`, []any{`"main" AND "str"*`}}, "contextual": {`SELECT rowid FROM entities INDEXED BY entities_exact_name WHERE normalized_name=? AND kind='area' AND closed=0 LIMIT 50`, []any{"portland"}}, "viewport": {`SELECT e.rowid FROM entity_fts JOIN entities e ON e.rowid=entity_fts.rowid JOIN entity_rtree r ON r.rowid=e.rowid WHERE entity_fts MATCH ? AND r.max_lat>=? AND r.min_lat<=? AND r.max_lng>=? AND r.min_lng<=? LIMIT 50`, []any{`"ups" AND "st"*`, 41.8, 41.9, -87.7, -87.5}}}
	out := map[string][]string{}
	for name, x := range queries {
		rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+x.q, x.a...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				return nil, err
			}
			out[name] = append(out[name], detail)
		}
		rows.Close()
	}
	return out, nil
}
