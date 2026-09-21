// Package opensearch implements the opt-in OpenSearch Places Autocomplete
// experiment. It is deliberately independent of the production DuckDB lookup
// selection and server paths.
package opensearch

import "encoding/json"

const (
	DefaultIndex   = "openmaps-places-autocomplete-v5"
	MappingVersion = 4
)

// IndexDefinition is intentionally explicit. dynamic=strict makes a changed
// exporter fail instead of silently changing the experimental contract.
func IndexDefinition() json.RawMessage {
	return json.RawMessage(`{
  "settings": {
    "number_of_shards": 1,
    "number_of_replicas": 0,
    "refresh_interval": "-1",
    "index.codec": "best_compression"
  },
  "mappings": {
    "dynamic": "strict",
    "_source": {"enabled": false},
    "_meta": {"openmaps_mapping_version": 4},
    "properties": {
      "id": {"type": "keyword", "index": false, "doc_values": false},
      "kind": {"type": "keyword"},
      "subtype": {"type": "keyword"},
      "name": {"type": "text"},
      "normalized_name": {"type": "keyword"},
      "aliases": {"type": "text"},
      "formatted_address": {"type": "text"},
      "normalized_address": {"type": "keyword"},
      "locality": {"type": "keyword"},
      "region": {"type": "keyword"},
      "region_code": {"type": "keyword"},
      "country": {"type": "keyword"},
      "postal_code": {"type": "keyword"},
      "hierarchy": {"type": "text"},
      "search_text": {"type": "search_as_you_type", "max_shingle_size": 3},
      "location": {"type": "geo_point"},
      "closed": {"type": "boolean"},
      "area_prominence": {"type": "short"},
      "settlement_tier": {"type": "byte"},
      "destination_class": {"type": "byte"},
      "specificity": {"type": "short"},
      "confidence_tier": {"type": "byte"},
      "area_override": {"type": "boolean"}
    }
  }
}`)
}

type Document struct {
	ID                string   `json:"id"`
	Kind              string   `json:"kind"`
	Subtype           string   `json:"subtype"`
	Name              string   `json:"name"`
	NormalizedName    string   `json:"normalized_name"`
	Aliases           []string `json:"aliases"`
	FormattedAddress  string   `json:"formatted_address"`
	NormalizedAddress string   `json:"normalized_address"`
	Locality          string   `json:"locality"`
	Region            string   `json:"region"`
	RegionCode        string   `json:"region_code"`
	Country           string   `json:"country"`
	PostalCode        string   `json:"postal_code"`
	Hierarchy         string   `json:"hierarchy"`
	SearchText        string   `json:"search_text"`
	Location          GeoPoint `json:"location"`
	Closed            bool     `json:"closed"`
	AreaProminence    int      `json:"area_prominence"`
	SettlementTier    uint8    `json:"settlement_tier"`
	DestinationClass  uint8    `json:"destination_class"`
	Specificity       int      `json:"specificity"`
	ConfidenceTier    uint8    `json:"confidence_tier"`
	AreaOverride      bool     `json:"area_override"`
}

type GeoPoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}
