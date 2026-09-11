// Package importer owns provider adaptation and provider-independent identity,
// conflict, provenance, and relationship rules. It never translates Google
// wire types or chooses a serving database.
package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"

	"openmaps/internal/places"
)

type Record struct {
	Source       string                     `json:"source"`
	SourceID     string                     `json:"source_id"`
	Release      string                     `json:"release"`
	Kind         string                     `json:"kind"`
	Priority     int                        `json:"priority"`
	Attributes   map[string]json.RawMessage `json:"attributes"`
	Paths        map[string]string          `json:"paths"`
	Raw          json.RawMessage            `json:"raw"`
	Attributions []places.Attribution       `json:"attributions"`
}

func (r Record) Key() string { return r.Source + ":" + r.SourceID }

type Relationship struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Kind     string `json:"kind"`
	Evidence string `json:"evidence"`
}
type Rejection struct {
	SourceKey string          `json:"source_key"`
	Reason    string          `json:"reason"`
	Raw       json.RawMessage `json:"raw"`
}
type Bundle struct {
	Schema        int               `json:"schema"`
	Manifest      json.RawMessage   `json:"manifest"`
	Identities    map[string]string `json:"identities"`
	Records       []Record          `json:"records"`
	Relationships []Relationship    `json:"relationships"`
	Rejections    []Rejection       `json:"rejections,omitempty"`
}

// ResolvedEntity is one provider-independent entity after identity and
// attribute conflict resolution. It is exposed for maintained artifact writers;
// Google wire formats remain confined to the API package.
type ResolvedEntity struct {
	ID, Kind, Name, NormalizedName, Address, Website, Subtype string
	Location                                                  places.Location
	Closed                                                    bool
	Attributions                                              []places.Attribution
	Aliases                                                   []string
}
type ResolvedSource struct {
	EntityID string
	Record   Record
}
type ResolvedProvenance struct {
	EntityID, Attribute, SourceKey, SourcePath string
}
type ResolvedRelationship struct {
	FromID, ToID, Kind, Evidence string
}
type ResolvedBundle struct {
	Manifest      json.RawMessage
	Identities    map[string]string
	Entities      []ResolvedEntity
	Sources       []ResolvedSource
	Provenance    []ResolvedProvenance
	Relationships []ResolvedRelationship
}

// PublicID bootstraps identity from an immutable, source-qualified anchor. It
// does not depend on attributes, release, import order, or transient row IDs.
func PublicID(anchor string) string {
	h := sha256.Sum256([]byte("openmaps:entity:v1:" + anchor))
	return "om_" + hex.EncodeToString(h[:16])
}
func serialized(v any) string { b, _ := json.Marshal(v); return string(b) }
func validLocation(p places.Location) bool {
	return !math.IsNaN(p.Lat) && !math.IsNaN(p.Lng) && !math.IsInf(p.Lat, 0) && !math.IsInf(p.Lng, 0) && p.Lat >= -90 && p.Lat <= 90 && p.Lng >= -180 && p.Lng <= 180
}

// normalize applies the identity, conflict, provenance and relationship rules.
func normalize(b Bundle) (ResolvedBundle, error) {
	var out ResolvedBundle
	var scope Manifest
	if err := json.Unmarshal(b.Manifest, &scope); err != nil {
		return out, err
	}
	if b.Schema != 1 || len(b.Records) == 0 || !json.Valid(b.Manifest) || string(b.Manifest) == "null" {
		return out, fmt.Errorf("unsupported or empty bundle")
	}
	out.Manifest = b.Manifest
	out.Identities = b.Identities
	groups := map[string][]Record{}
	ids := map[string]string{}
	kinds := map[string]string{}
	for _, r := range b.Records {
		if r.Source == "" || r.SourceID == "" || r.Release == "" || len(r.Raw) == 0 || len(r.Attributions) == 0 {
			return out, fmt.Errorf("incomplete provenance: %s", r.Key())
		}
		if _, ok := ids[r.Key()]; ok {
			return out, fmt.Errorf("duplicate source key: %s", r.Key())
		}
		anchor := r.Key()
		if target, ok := b.Identities[anchor]; ok {
			if target == "" {
				return out, fmt.Errorf("empty identity anchor")
			}
			if next, ok := b.Identities[target]; ok && next != target {
				return out, fmt.Errorf("identity chains are not allowed")
			}
			anchor = target
		}
		id := PublicID(anchor)
		ids[r.Key()] = id
		if kind, ok := kinds[id]; ok && kind != r.Kind {
			return out, fmt.Errorf("cross-kind identity merge: %s", anchor)
		}
		kinds[id] = r.Kind
		groups[id] = append(groups[id], r)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, id := range keys {
		rs := groups[id]
		sort.Slice(rs, func(i, j int) bool {
			if rs[i].Priority != rs[j].Priority {
				return rs[i].Priority > rs[j].Priority
			}
			return rs[i].Key() < rs[j].Key()
		})
		values := map[string]json.RawMessage{}
		winners := map[string]Record{}
		attrs := []places.Attribution{}
		attrSeen := map[string]bool{}
		for _, r := range rs {
			for k, v := range r.Attributes {
				switch k {
				case "name", "address", "website", "subtype", "location", "closed", "aliases":
				default:
					return out, fmt.Errorf("unknown normalized attribute: %s", k)
				}
				if string(v) == "null" || string(v) == `""` || string(v) == "[]" {
					continue
				}
				if r.Paths[k] == "" {
					return out, fmt.Errorf("missing attribute provenance: %s %s", r.Key(), k)
				}
				if _, ok := values[k]; !ok {
					values[k] = v
					winners[k] = r
				}
			}
			for _, a := range r.Attributions {
				k := serialized(a)
				if !attrSeen[k] {
					attrs = append(attrs, a)
					attrSeen[k] = true
				}
			}
		}
		var name, address, website, subtype string
		var p places.Location
		var closed bool
		var aliases []string
		for k, dst := range map[string]any{"name": &name, "address": &address, "website": &website, "subtype": &subtype, "location": &p, "closed": &closed, "aliases": &aliases} {
			if v, ok := values[k]; ok {
				if err := json.Unmarshal(v, dst); err != nil {
					return out, fmt.Errorf("attribute %s: %w", k, err)
				}
			}
		}
		if strings.TrimSpace(name) == "" || values["location"] == nil || !validLocation(p) {
			return out, fmt.Errorf("invalid name/location: %s", id)
		}
		var coordinates struct {
			Lat *float64 `json:"lat"`
			Lng *float64 `json:"lng"`
		}
		if err := json.Unmarshal(values["location"], &coordinates); err != nil || coordinates.Lat == nil || coordinates.Lng == nil {
			return out, fmt.Errorf("location requires both lat and lng: %s", id)
		}
		if website != "" {
			u, err := url.Parse(website)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return out, fmt.Errorf("invalid website: %s", id)
			}
		}
		out.Entities = append(out.Entities, ResolvedEntity{
			ID: id, Kind: rs[0].Kind, Name: name, NormalizedName: places.Normalize(name),
			Address: address, Website: website, Subtype: subtype, Location: p,
			Closed: closed, Attributions: attrs, Aliases: aliases,
		})
		for _, r := range rs {
			out.Sources = append(out.Sources, ResolvedSource{EntityID: id, Record: r})
		}
		winnerKeys := make([]string, 0, len(winners))
		for attribute := range winners {
			winnerKeys = append(winnerKeys, attribute)
		}
		sort.Strings(winnerKeys)
		for _, attribute := range winnerKeys {
			r := winners[attribute]
			out.Provenance = append(out.Provenance, ResolvedProvenance{id, attribute, r.Key(), r.Paths[attribute]})
		}
	}
	seenRelationships := map[string]bool{}
	for _, r := range b.Relationships {
		from, to := ids[r.From], ids[r.To]
		if from == "" || to == "" || from == to {
			return out, fmt.Errorf("invalid relationship: %+v", r)
		}
		if r.Kind == "address" && (kinds[from] != "business" || kinds[to] != "address") {
			return out, fmt.Errorf("invalid address relationship")
		}
		if r.Kind == "parent_area" && (kinds[from] != "area" || kinds[to] != "area") {
			return out, fmt.Errorf("invalid area relationship")
		}
		key := from + "\x00" + to + "\x00" + r.Kind
		if !seenRelationships[key] {
			out.Relationships = append(out.Relationships, ResolvedRelationship{from, to, r.Kind, r.Evidence})
			seenRelationships[key] = true
		}
	}
	sort.Slice(out.Relationships, func(i, j int) bool {
		a, b := out.Relationships[i], out.Relationships[j]
		if a.FromID != b.FromID {
			return a.FromID < b.FromID
		}
		if a.ToID != b.ToID {
			return a.ToID < b.ToID
		}
		return a.Kind < b.Kind
	})
	return out, nil
}

// Resolve validates and resolves a normalized source bundle without choosing a
// storage format. Artifact writers use it to share the production import rules.
func Resolve(b Bundle) (ResolvedBundle, error) {
	return normalize(b)
}

// ResolveEntityGroup applies the maintained attribute conflict and provenance
// rules to one identity group. Streaming artifact builders call it with one
// externally sorted group at a time, so memory is bounded by the number of
// source records contributing to a single public entity rather than the size
// of the snapshot.
func ResolveEntityGroup(id string, records []Record) (ResolvedEntity, []ResolvedSource, []ResolvedProvenance, error) {
	var entity ResolvedEntity
	if id == "" || len(records) == 0 {
		return entity, nil, nil, fmt.Errorf("empty entity group")
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Priority != records[j].Priority {
			return records[i].Priority > records[j].Priority
		}
		return records[i].Key() < records[j].Key()
	})
	values := map[string]json.RawMessage{}
	winners := map[string]Record{}
	attrs := []places.Attribution{}
	attrSeen := map[string]bool{}
	seenSources := map[string]bool{}
	for _, r := range records {
		if r.Source == "" || r.SourceID == "" || r.Release == "" || len(r.Raw) == 0 || len(r.Attributions) == 0 {
			return entity, nil, nil, fmt.Errorf("incomplete provenance: %s", r.Key())
		}
		if seenSources[r.Key()] {
			return entity, nil, nil, fmt.Errorf("duplicate source key: %s", r.Key())
		}
		seenSources[r.Key()] = true
		if r.Kind != records[0].Kind {
			return entity, nil, nil, fmt.Errorf("cross-kind identity merge: %s", id)
		}
		for k, v := range r.Attributes {
			switch k {
			case "name", "address", "website", "subtype", "location", "closed", "aliases":
			default:
				return entity, nil, nil, fmt.Errorf("unknown normalized attribute: %s", k)
			}
			if string(v) == "null" || string(v) == `""` || string(v) == "[]" {
				continue
			}
			if r.Paths[k] == "" {
				return entity, nil, nil, fmt.Errorf("missing attribute provenance: %s %s", r.Key(), k)
			}
			if _, ok := values[k]; !ok {
				values[k] = v
				winners[k] = r
			}
		}
		for _, a := range r.Attributions {
			key := serialized(a)
			if !attrSeen[key] {
				attrs = append(attrs, a)
				attrSeen[key] = true
			}
		}
	}
	var name, address, website, subtype string
	var point places.Location
	var closed bool
	var aliases []string
	for key, dst := range map[string]any{"name": &name, "address": &address, "website": &website, "subtype": &subtype, "location": &point, "closed": &closed, "aliases": &aliases} {
		if value, ok := values[key]; ok {
			if err := json.Unmarshal(value, dst); err != nil {
				return entity, nil, nil, fmt.Errorf("attribute %s: %w", key, err)
			}
		}
	}
	if strings.TrimSpace(name) == "" || values["location"] == nil || !validLocation(point) {
		return entity, nil, nil, fmt.Errorf("invalid name/location: %s", id)
	}
	var coordinates struct {
		Lat *float64 `json:"lat"`
		Lng *float64 `json:"lng"`
	}
	if err := json.Unmarshal(values["location"], &coordinates); err != nil || coordinates.Lat == nil || coordinates.Lng == nil {
		return entity, nil, nil, fmt.Errorf("location requires both lat and lng: %s", id)
	}
	if website != "" {
		u, err := url.Parse(website)
		if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
			return entity, nil, nil, fmt.Errorf("invalid website: %s", id)
		}
	}
	entity = ResolvedEntity{ID: id, Kind: records[0].Kind, Name: name, NormalizedName: places.Normalize(name), Address: address, Website: website, Subtype: subtype, Location: point, Closed: closed, Attributions: attrs, Aliases: aliases}
	sources := make([]ResolvedSource, 0, len(records))
	for _, record := range records {
		sources = append(sources, ResolvedSource{EntityID: id, Record: record})
	}
	keys := make([]string, 0, len(winners))
	for key := range winners {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	provenance := make([]ResolvedProvenance, 0, len(keys))
	for _, attribute := range keys {
		record := winners[attribute]
		provenance = append(provenance, ResolvedProvenance{EntityID: id, Attribute: attribute, SourceKey: record.Key(), SourcePath: record.Paths[attribute]})
	}
	return entity, sources, provenance, nil
}
