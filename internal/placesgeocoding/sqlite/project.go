package sqlite

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"openmaps/internal/importer"
	"openmaps/internal/places"
)

type entityRow struct {
	ID                string  `parquet:"id"`
	Kind              string  `parquet:"kind"`
	Name              string  `parquet:"name"`
	NormalizedName    string  `parquet:"normalized_name"`
	Address           string  `parquet:"address"`
	NormalizedAddress string  `parquet:"normalized_address"`
	NormalizedAliases string  `parquet:"normalized_aliases"`
	Subtype           string  `parquet:"subtype"`
	Lat               float64 `parquet:"lat"`
	Lng               float64 `parquet:"lng"`
	Closed            bool    `parquet:"closed"`
	SourceStart       int64   `parquet:"source_start"`
	SourceCount       int64   `parquet:"source_count"`
	SourceFile        string  `parquet:"source_file"`
}

type sourceRow struct {
	EntityID   string `parquet:"entity_id"`
	SourceKey  string `parquet:"source_key"`
	Source     string `parquet:"source"`
	Priority   int64  `parquet:"priority"`
	Attributes string `parquet:"attributes"`
	Raw        string `parquet:"raw"`
}

type document struct {
	ID, Kind, Subtype, Name, NormalizedName string
	Aliases                                 string
	FormattedAddress, NormalizedAddress     string
	Locality, Region, RegionCode            string
	Country, PostalCode, Hierarchy          string
	Lat, Lng                                float64
	Closed                                  bool
	AreaProminence, SettlementTier          int
	DestinationClass, Specificity           int
	ConfidenceTier                          int
	AreaOverride                            bool
}

func project(row entityRow, sources []sourceRow) (document, error) {
	doc := document{ID: row.ID, Kind: row.Kind, Subtype: row.Subtype, Name: row.Name,
		NormalizedName: row.NormalizedName, Aliases: row.NormalizedAliases,
		FormattedAddress: row.Address, NormalizedAddress: row.NormalizedAddress,
		Lat: row.Lat, Lng: row.Lng, Closed: row.Closed}
	sort.SliceStable(sources, func(i, j int) bool {
		if sources[i].Priority != sources[j].Priority {
			return sources[i].Priority > sources[j].Priority
		}
		return sources[i].SourceKey < sources[j].SourceKey
	})
	for _, source := range sources {
		if source.EntityID != row.ID {
			return doc, fmt.Errorf("source locator mismatch: got %s want %s", source.EntityID, row.ID)
		}
		if doc.Aliases == "" {
			var attrs map[string]json.RawMessage
			if err := json.Unmarshal([]byte(source.Attributes), &attrs); err != nil {
				return doc, err
			}
			var aliases []string
			_ = json.Unmarshal(attrs["aliases"], &aliases)
			normalized := make([]string, 0, len(aliases))
			for _, alias := range aliases {
				if value := places.Normalize(alias); value != "" {
					normalized = append(normalized, value)
				}
			}
			doc.Aliases = strings.Join(normalized, " ")
		}
		if doc.AreaProminence == 0 && doc.SettlementTier == 0 {
			evidence, err := importer.AreaRankingEvidence(source.Source, json.RawMessage(source.Raw))
			if err != nil {
				return doc, err
			}
			doc.AreaProminence, doc.SettlementTier = evidence.Prominence, int(evidence.SettlementTier)
		}
		if doc.DestinationClass == 0 && doc.Specificity == 0 && doc.ConfidenceTier == 0 {
			evidence, err := importer.PlaceRankingEvidence(source.Source, json.RawMessage(source.Raw))
			if err != nil {
				return doc, err
			}
			doc.DestinationClass, doc.Specificity = int(evidence.DestinationClass), evidence.Specificity
			doc.ConfidenceTier, doc.AreaOverride = int(evidence.ConfidenceTier), evidence.AreaOverride
		}
		if doc.Locality == "" && doc.Region == "" && doc.Country == "" {
			projectContext(&doc, []byte(source.Raw))
		}
	}
	doc.Hierarchy = strings.Join(nonempty([]string{doc.Locality, doc.Region, doc.RegionCode, doc.Country, doc.PostalCode}), " ")
	return doc, nil
}

func projectContext(doc *document, raw []byte) {
	var feature struct {
		Properties struct {
			Postcode      string `json:"postcode"`
			Country       string `json:"country"`
			AddressLevels []struct {
				Value string `json:"value"`
			} `json:"address_levels"`
			Addresses []struct{ Locality, Region, Country, Postcode string } `json:"addresses"`
		} `json:"properties"`
	}
	if json.Unmarshal(raw, &feature) != nil {
		return
	}
	p := feature.Properties
	if len(p.Addresses) > 0 {
		a := p.Addresses[0]
		doc.Locality, doc.Region, doc.Country, doc.PostalCode = a.Locality, a.Region, a.Country, a.Postcode
		if len(a.Region) == 2 {
			doc.RegionCode = strings.ToUpper(a.Region)
		}
		return
	}
	doc.Country, doc.PostalCode = p.Country, p.Postcode
	if p.Country == "US" && len(p.AddressLevels) == 2 {
		doc.Region, doc.Locality = p.AddressLevels[0].Value, p.AddressLevels[1].Value
		if len(doc.Region) == 2 {
			doc.RegionCode = strings.ToUpper(doc.Region)
		}
	}
}

func nonempty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
