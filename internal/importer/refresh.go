package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"openmaps/internal/places"
)

// Identity remembers even absent source keys. An absent entity is unavailable,
// not asserted closed; its ID is never assigned to a different anchor.
type Identity struct {
	Anchor string `json:"anchor"`
	Kind   string `json:"kind"`
}
type Replacement struct {
	Old      string `json:"old"`
	New      string `json:"new"`
	Evidence string `json:"evidence"`
	Reviewer string `json:"reviewer"`
}
type EntityState struct {
	places.Entity
	Closed bool
}
type SourceState struct {
	ID         string          `json:"public_id"`
	Release    string          `json:"release"`
	Priority   int             `json:"priority"`
	Attributes json.RawMessage `json:"attributes"`
	Paths      json.RawMessage `json:"paths"`
	Raw        json.RawMessage `json:"raw"`
}
type Snapshot struct {
	Manifest      json.RawMessage
	Identities    map[string]string
	History       map[string]Identity
	Entities      map[string]EntityState
	Sources       map[string]SourceState
	Relationships []Relationship
}

func openSnapshot(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// ReadSnapshot validates the SQLite structure, references, source anchors, and
// searchable row coverage before returning a deterministic logical snapshot.
func ReadSnapshot(ctx context.Context, path string) (s Snapshot, err error) {
	s = Snapshot{Identities: map[string]string{}, History: map[string]Identity{}, Entities: map[string]EntityState{}, Sources: map[string]SourceState{}, Relationships: []Relationship{}}
	db, e := openSnapshot(path)
	if e != nil {
		return s, e
	}
	defer db.Close()
	var check string
	if e = db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); e != nil {
		return s, e
	}
	if check != "ok" {
		return s, fmt.Errorf("integrity: %s", check)
	}
	rows, e := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if e != nil {
		return s, e
	}
	bad := rows.Next()
	e = rows.Err()
	rows.Close()
	if e != nil {
		return s, e
	}
	if bad {
		return s, fmt.Errorf("foreign key check failed")
	}
	var version string
	if e = db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='schema_version'").Scan(&version); e != nil || version != "1" {
		return s, fmt.Errorf("unsupported schema: %s: %v", version, e)
	}
	for k, dst := range map[string]any{"manifest": &s.Manifest, "identities": &s.Identities, "identity_history": &s.History} {
		var value string
		e = db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key=?", k).Scan(&value)
		if e == sql.ErrNoRows && k == "identity_history" {
			continue
		}
		if e != nil {
			return s, e
		}
		if e = json.Unmarshal([]byte(value), dst); e != nil {
			return s, e
		}
	}
	if s.Identities == nil {
		s.Identities = map[string]string{}
	}
	if s.History == nil {
		s.History = map[string]Identity{}
	}
	rows, e = db.QueryContext(ctx, "SELECT id,kind,name,address,website,subtype,lat,lng,closed,attributions FROM entities ORDER BY id")
	if e != nil {
		return s, e
	}
	for rows.Next() {
		var v EntityState
		var attrs string
		if e = rows.Scan(&v.ID, &v.Kind, &v.Name, &v.Address, &v.Website, &v.Subtype, &v.Location.Lat, &v.Location.Lng, &v.Closed, &attrs); e != nil {
			rows.Close()
			return s, e
		}
		if e = json.Unmarshal([]byte(attrs), &v.Attributions); e != nil {
			rows.Close()
			return s, e
		}
		s.Entities[v.ID] = v
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return s, e
	}
	var scope Manifest
	if e = json.Unmarshal(s.Manifest, &scope); e != nil {
		return s, e
	}
	if len(s.Entities) == 0 {
		return s, fmt.Errorf("empty snapshot")
	}
	rows, e = db.QueryContext(ctx, "SELECT source_key,entity_id,release,priority,attributes,paths,raw FROM source_records ORDER BY source_key")
	if e != nil {
		return s, e
	}
	for rows.Next() {
		var key, attrs, paths, raw string
		var v SourceState
		if e = rows.Scan(&key, &v.ID, &v.Release, &v.Priority, &attrs, &paths, &raw); e != nil {
			rows.Close()
			return s, e
		}
		v.Attributes = json.RawMessage(attrs)
		v.Paths = json.RawMessage(paths)
		v.Raw = json.RawMessage(raw)
		s.Sources[key] = v
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return s, e
	}
	referenced := map[string]bool{}
	for key, v := range s.Sources {
		anchor := key
		if a, ok := s.Identities[key]; ok {
			anchor = a
		}
		entity, ok := s.Entities[v.ID]
		if !ok || PublicID(anchor) != v.ID {
			return s, fmt.Errorf("invalid identity: %s", key)
		}
		h := Identity{anchor, entity.Kind}
		if old, ok := s.History[key]; ok && old != h {
			return s, fmt.Errorf("inconsistent identity history: %s", key)
		}
		s.History[key] = h
		referenced[v.ID] = true
	}
	if len(referenced) != len(s.Entities) {
		return s, fmt.Errorf("entity without source")
	}
	rows, e = db.QueryContext(ctx, "SELECT from_id,to_id,kind,evidence FROM relationships ORDER BY from_id,to_id,kind")
	if e != nil {
		return s, e
	}
	for rows.Next() {
		var r Relationship
		if e = rows.Scan(&r.From, &r.To, &r.Kind, &r.Evidence); e != nil {
			rows.Close()
			return s, e
		}
		s.Relationships = append(s.Relationships, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return s, e
	}
	var invalid int
	e = db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM entities e LEFT JOIN entity_fts f ON e.rowid=f.rowid WHERE f.rowid IS NULL OR f.name!=e.normalized_name) + (SELECT count(*) FROM entity_fts f LEFT JOIN entities e ON e.rowid=f.rowid WHERE e.id IS NULL)`).Scan(&invalid)
	if e != nil {
		return s, e
	}
	if invalid != 0 {
		return s, fmt.Errorf("FTS coverage/name mismatch: %d", invalid)
	}
	rows, e = db.QueryContext(ctx, `SELECT e.id,e.normalized_name,f.name,f.address,f.aliases,coalesce(sr.attributes,'{}')
 FROM entities e JOIN entity_fts f ON e.rowid=f.rowid
 LEFT JOIN attribute_provenance p ON p.entity_id=e.id AND p.attribute='aliases'
 LEFT JOIN source_records sr ON sr.source_key=p.source_key`)
	if e != nil {
		return s, e
	}
	for rows.Next() {
		var id, normalized, name, address, aliases, attrs string
		if e = rows.Scan(&id, &normalized, &name, &address, &aliases, &attrs); e != nil {
			rows.Close()
			return s, e
		}
		var values struct {
			Aliases []string `json:"aliases"`
		}
		if e = json.Unmarshal([]byte(attrs), &values); e != nil {
			rows.Close()
			return s, e
		}
		v := s.Entities[id]
		if normalized != places.Normalize(v.Name) || name != normalized || address != places.Normalize(v.Address) || aliases != places.Normalize(strings.Join(values.Aliases, " ")) {
			rows.Close()
			return s, fmt.Errorf("FTS content mismatch: %s", id)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return s, e
	}
	return s, nil
}

// Reconcile permits only reviewed one-to-one provider replacements. Splits and
// merges retain surviving source identities and give new records distinct IDs.
// Historical mappings cannot be dropped, retargeted, or reused across kinds.
func Reconcile(b Bundle, base Snapshot, replacements []Replacement) (Bundle, map[string]Identity, error) {
	ids := map[string]string{}
	for k, v := range b.Identities {
		ids[k] = v
	}
	b.Identities = ids
	history := map[string]Identity{}
	for k, v := range base.History {
		history[k] = v
	}
	for k, v := range base.Identities {
		if x, ok := ids[k]; ok && x != v {
			return b, nil, fmt.Errorf("retargeted historical mapping: %s", k)
		}
		ids[k] = v
	}
	records := map[string]Record{}
	for _, r := range b.Records {
		records[r.Key()] = r
	}
	usedOld, usedNew := map[string]bool{}, map[string]bool{}
	for _, r := range replacements {
		old, ok := base.Sources[r.Old]
		next, newOK := records[r.New]
		_, oldPresent := records[r.Old]
		_, newKnown := history[r.New]
		if !ok || !newOK || oldPresent || newKnown || usedOld[old.ID] || usedNew[r.New] || strings.TrimSpace(r.Evidence) == "" || strings.TrimSpace(r.Reviewer) == "" {
			return b, nil, fmt.Errorf("replacement must be reviewed, new, absent-old and one-to-one: %+v", r)
		}
		if base.Entities[old.ID].Kind != next.Kind {
			return b, nil, fmt.Errorf("cross-kind replacement")
		}
		// Other surviving source records would turn this into a merge/enrichment.
		for k, v := range base.Sources {
			if v.ID == old.ID {
				if _, ok := records[k]; ok {
					return b, nil, fmt.Errorf("replacement entity still has a source: %s", k)
				}
			}
		}
		anchor := history[r.Old].Anchor
		if a, ok := ids[r.New]; ok && a != anchor {
			return b, nil, fmt.Errorf("conflicting replacement mapping")
		}
		ids[r.New] = anchor
		usedOld[old.ID] = true
		usedNew[r.New] = true
	}
	for k, a := range ids {
		if old, ok := history[k]; ok && old.Anchor != a {
			return b, nil, fmt.Errorf("changed historical anchor: %s", k)
		}
	}
	for _, r := range b.Records {
		anchor := r.Key()
		if a, ok := ids[r.Key()]; ok {
			anchor = a
		}
		if old, ok := history[r.Key()]; ok {
			if anchor != old.Anchor || r.Kind != old.Kind {
				return b, nil, fmt.Errorf("continuing source changed identity/kind: %s", r.Key())
			}
		} else if anchor != r.Key() && !usedNew[r.Key()] {
			return b, nil, fmt.Errorf("new identity mapping requires a reviewed replacement: %s", r.Key())
		}
		history[r.Key()] = Identity{anchor, r.Kind}
	}
	return b, history, nil
}

// SaveRefreshMetadata runs only on an unpublished new database.
func SaveRefreshMetadata(path string, history map[string]Identity, replacements []Replacement, bundleHash string) error {
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return e
	}
	defer db.Close()
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for k, v := range map[string]any{"identity_history": history, "replacements": replacements, "input_bundle_sha256": bundleHash} {
		if _, e = tx.Exec("INSERT INTO metadata VALUES(?,?)", k, serialized(v)); e != nil {
			return e
		}
	}
	return tx.Commit()
}

type QueryCheck struct {
	Input     string `json:"input"`
	FirstID   string `json:"first_id,omitempty"`
	FirstKind string `json:"first_kind,omitempty"`
	Empty     bool   `json:"empty,omitempty"`
}

// NewportPlacesQueryChecks returns the maintained Places autocomplete smoke
// checks used for refresh comparisons and live integration verification.
func NewportPlacesQueryChecks() []QueryCheck {
	return []QueryCheck{
		{Input: "White Horse", FirstID: "om_a5e3dc7692e4d3b90b71b94fba66ec5b", FirstKind: "business"},
		{Input: "50 Bellevue", FirstID: "om_f88c096070879478ee02e036d3e67480", FirstKind: "address"},
		{Input: "Thames", FirstID: "om_0953764bc3686bc97c659471619e9dfa", FirstKind: "street"},
		{Input: "Newport", FirstKind: "area"},
		{Input: "Redwood Library", FirstID: "om_6393fc0fc62fcba159e53e572c6cbbbd", FirstKind: "business"},
		{Input: "26 Marlborough", FirstKind: "address"},
		{Input: "zzxnoresult", Empty: true},
		{Input: "Pearl Car Wash", FirstID: "om_ef8f5f0dc3fcde9f13af6cbdce15c271", FirstKind: "business"},
	}
}

type QueryResult struct {
	Check  QueryCheck      `json:"check"`
	Before []places.Entity `json:"before"`
	After  []places.Entity `json:"after"`
}
type EntityChange struct {
	ID     string      `json:"id"`
	Fields []string    `json:"fields"`
	Before EntityState `json:"before"`
	After  EntityState `json:"after"`
}
type SourceChange struct {
	Key    string   `json:"key"`
	Fields []string `json:"fields"`
}
type ReviewMatch struct {
	BeforeID string  `json:"before_id"`
	AfterID  string  `json:"after_id"`
	Reason   string  `json:"reason"`
	Metres   float64 `json:"metres"`
}
type Report struct {
	Schema               int             `json:"schema"`
	BaselineSHA256       string          `json:"baseline_sha256"`
	CandidateSHA256      string          `json:"candidate_sha256"`
	BaselineManifest     json.RawMessage `json:"baseline_manifest"`
	CandidateManifest    json.RawMessage `json:"candidate_manifest"`
	BeforeCounts         map[string]int  `json:"before_counts"`
	AfterCounts          map[string]int  `json:"after_counts"`
	ContinuingIDs        int             `json:"continuing_ids"`
	Added                []EntityState   `json:"added"`
	Removed              []EntityState   `json:"removed"`
	Changed              []EntityChange  `json:"changed"`
	SourcesAdded         []string        `json:"sources_added"`
	SourcesRemoved       []string        `json:"sources_removed"`
	SourcesChanged       []SourceChange  `json:"sources_changed"`
	RelationshipsAdded   []Relationship  `json:"relationships_added"`
	RelationshipsRemoved []Relationship  `json:"relationships_removed"`
	ReviewMatches        []ReviewMatch   `json:"review_matches"`
	Queries              []QueryResult   `json:"queries"`
	Violations           []string        `json:"violations"`
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Compare is offline. It reports every disappearance as absence from this
// regional snapshot, never as evidence of a real-world closure or deletion.
func Compare(ctx context.Context, baseline, candidate string, checks []QueryCheck) (Report, error) {
	r := Report{Schema: 1, BeforeCounts: map[string]int{}, AfterCounts: map[string]int{}, Added: []EntityState{}, Removed: []EntityState{}, Changed: []EntityChange{}, SourcesAdded: []string{}, SourcesRemoved: []string{}, SourcesChanged: []SourceChange{}, RelationshipsAdded: []Relationship{}, RelationshipsRemoved: []Relationship{}, ReviewMatches: []ReviewMatch{}, Queries: []QueryResult{}, Violations: []string{}}
	a, e := ReadSnapshot(ctx, baseline)
	if e != nil {
		return r, e
	}
	b, e := ReadSnapshot(ctx, candidate)
	if e != nil {
		return r, e
	}
	if r.BaselineSHA256, e = Checksum(baseline); e != nil {
		return r, e
	}
	if r.CandidateSHA256, e = Checksum(candidate); e != nil {
		return r, e
	}
	r.BaselineManifest = a.Manifest
	r.CandidateManifest = b.Manifest
	for _, v := range a.Entities {
		r.BeforeCounts[v.Kind]++
	}
	for _, v := range b.Entities {
		r.AfterCounts[v.Kind]++
	}
	for _, id := range sortedKeys(a.Entities) {
		old := a.Entities[id]
		next, ok := b.Entities[id]
		if !ok {
			r.Removed = append(r.Removed, old)
			continue
		}
		r.ContinuingIDs++
		fields := []string{}
		for _, f := range []struct {
			name string
			a, b any
		}{{"kind", old.Kind, next.Kind}, {"name", old.Name, next.Name}, {"address", old.Address, next.Address}, {"website", old.Website, next.Website}, {"subtype", old.Subtype, next.Subtype}, {"location", old.Location, next.Location}, {"closed", old.Closed, next.Closed}, {"attributions", old.Attributions, next.Attributions}} {
			if !reflect.DeepEqual(f.a, f.b) {
				fields = append(fields, f.name)
			}
		}
		if old.Kind != next.Kind {
			r.Violations = append(r.Violations, "continuing entity changed kind: "+id)
		}
		if len(fields) > 0 {
			r.Changed = append(r.Changed, EntityChange{id, fields, old, next})
		}
	}
	for _, id := range sortedKeys(b.Entities) {
		if _, ok := a.Entities[id]; !ok {
			r.Added = append(r.Added, b.Entities[id])
		}
	}
	for _, key := range sortedKeys(a.Sources) {
		old := a.Sources[key]
		next, ok := b.Sources[key]
		if !ok {
			r.SourcesRemoved = append(r.SourcesRemoved, key)
			continue
		}
		fields := []string{}
		if old.ID != next.ID {
			r.Violations = append(r.Violations, "continuing source changed public ID: "+key)
			fields = append(fields, "public_id")
		}
		if old.Release != next.Release {
			fields = append(fields, "release")
		}
		if old.Priority != next.Priority {
			fields = append(fields, "priority")
		}
		for _, f := range []struct {
			name string
			a, b json.RawMessage
		}{{"attributes", old.Attributes, next.Attributes}, {"paths", old.Paths, next.Paths}, {"raw", old.Raw, next.Raw}} {
			if !sameJSON(f.a, f.b) {
				fields = append(fields, f.name)
			}
		}
		if len(fields) > 0 {
			r.SourcesChanged = append(r.SourcesChanged, SourceChange{key, fields})
		}
	}
	for _, key := range sortedKeys(b.Sources) {
		if _, ok := a.Sources[key]; !ok {
			r.SourcesAdded = append(r.SourcesAdded, key)
		}
	}
	for _, key := range sortedKeys(a.History) {
		old := a.History[key]
		if next, ok := b.History[key]; !ok || next != old {
			r.Violations = append(r.Violations, "lost or changed identity history: "+key)
		}
	}
	// Validate new aliases against the evidence retained with the candidate.
	db, e := openSnapshot(candidate)
	if e != nil {
		return r, e
	}
	var decisions string
	var replacements []Replacement
	e = db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='replacements'").Scan(&decisions)
	db.Close()
	if e != nil && e != sql.ErrNoRows {
		return r, e
	}
	if e == nil {
		if e = json.Unmarshal([]byte(decisions), &replacements); e != nil {
			return r, e
		}
	}
	records := []Record{}
	for _, key := range sortedKeys(b.Sources) {
		source := b.Sources[key]
		i := strings.LastIndex(key, ":")
		if i < 1 {
			return r, fmt.Errorf("unqualified source key")
		}
		records = append(records, Record{Source: key[:i], SourceID: key[i+1:], Kind: b.Entities[source.ID].Kind})
	}
	if _, _, e = Reconcile(Bundle{Identities: b.Identities, Records: records}, a, replacements); e != nil {
		r.Violations = append(r.Violations, e.Error())
	}
	oldR, newR := map[string]Relationship{}, map[string]Relationship{}
	for _, v := range a.Relationships {
		oldR[serialized(v)] = v
	}
	for _, v := range b.Relationships {
		newR[serialized(v)] = v
	}
	for _, k := range sortedKeys(oldR) {
		if _, ok := newR[k]; !ok {
			r.RelationshipsRemoved = append(r.RelationshipsRemoved, oldR[k])
		}
	}
	for _, k := range sortedKeys(newR) {
		if _, ok := oldR[k]; !ok {
			r.RelationshipsAdded = append(r.RelationshipsAdded, newR[k])
		}
	}
	// A new or absent entity near a similarly labelled entity is only a review
	// lead. Include surviving counterparts to expose possible splits/merges.
	newIDs := sortedKeys(b.Entities)
	addedIDs := []string{}
	for _, v := range r.Added {
		addedIDs = append(addedIDs, v.ID)
	}
	for _, oldID := range sortedKeys(a.Entities) {
		old := a.Entities[oldID]
		_, survives := b.Entities[oldID]
		candidates := newIDs
		if survives {
			candidates = addedIDs
		}
		for _, newID := range candidates {
			next := b.Entities[newID]
			_, existed := a.Entities[newID]
			if oldID == newID || old.Kind != next.Kind || (survives && existed) {
				continue
			}
			metres := distance([2]float64{old.Location.Lng, old.Location.Lat}, [2]float64{next.Location.Lng, next.Location.Lat})
			if metres > 50 {
				continue
			}
			reason := ""
			if places.Normalize(old.Name) == places.Normalize(next.Name) {
				reason = "same normalized name within 50 metres"
			} else if old.Kind == "business" && old.Website != "" && old.Website == next.Website {
				reason = "same website within 50 metres"
			}
			if reason != "" {
				r.ReviewMatches = append(r.ReviewMatches, ReviewMatch{oldID, newID, reason, metres})
			}
		}
	}
	baseline, e = filepath.Abs(baseline)
	if e != nil {
		return r, e
	}
	candidate, e = filepath.Abs(candidate)
	if e != nil {
		return r, e
	}
	as, e := places.Open(baseline)
	if e != nil {
		return r, e
	}
	defer as.Close()
	bs, e := places.Open(candidate)
	if e != nil {
		return r, e
	}
	defer bs.Close()
	if len(checks) == 0 {
		r.Violations = append(r.Violations, "representative query checks are required")
	}
	for _, q := range checks {
		before, e := as.Autocomplete(ctx, q.Input)
		if e != nil {
			return r, e
		}
		after, e := bs.Autocomplete(ctx, q.Input)
		if e != nil {
			return r, e
		}
		r.Queries = append(r.Queries, QueryResult{q, before, after})
		if strings.TrimSpace(q.Input) == "" || (!q.Empty && q.FirstID == "" && q.FirstKind == "") {
			r.Violations = append(r.Violations, "query requires an expectation: "+q.Input)
		}
		if q.Empty {
			if len(after) != 0 {
				r.Violations = append(r.Violations, "expected empty query: "+q.Input)
			}
		} else if len(after) == 0 || (q.FirstID != "" && after[0].ID != q.FirstID) || (q.FirstKind != "" && after[0].Kind != q.FirstKind) {
			r.Violations = append(r.Violations, "first result failed: "+q.Input)
		}
		for _, v := range after {
			detail, e := bs.Details(ctx, v.ID)
			if e != nil {
				return r, e
			}
			if !reflect.DeepEqual(detail, v) {
				r.Violations = append(r.Violations, "details mismatch: "+v.ID)
			}
		}
	}
	return r, nil
}
