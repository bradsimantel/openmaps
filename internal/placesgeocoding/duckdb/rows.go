package duckdb

type entityRow struct {
	ID                string  `parquet:"id"`
	Kind              string  `parquet:"kind"`
	Name              string  `parquet:"name"`
	NormalizedName    string  `parquet:"normalized_name"`
	Address           string  `parquet:"address"`
	NormalizedAddress string  `parquet:"normalized_address"`
	NormalizedAliases string  `parquet:"normalized_aliases"`
	Website           string  `parquet:"website"`
	Subtype           string  `parquet:"subtype"`
	Lat               float64 `parquet:"lat"`
	Lng               float64 `parquet:"lng"`
	Closed            bool    `parquet:"closed"`
	Attributions      string  `parquet:"attributions"`
	EntityGroup       int64   `parquet:"entity_row_group"`
	EntityRow         int64   `parquet:"entity_row"`
	SourceStart       int64   `parquet:"source_start"`
	SourceCount       int64   `parquet:"source_count"`
	SourceFile        string  `parquet:"source_file"`
	ProvenanceStart   int64   `parquet:"provenance_start"`
	ProvenanceCount   int64   `parquet:"provenance_count"`
	ProvenanceFile    string  `parquet:"provenance_file"`
	AddressKey        string  `parquet:"address_key"`
	AddressContext    string  `parquet:"address_context"`
}

type sourceRow struct {
	EntityID   string `parquet:"entity_id"`
	SourceKey  string `parquet:"source_key"`
	Source     string `parquet:"source"`
	SourceID   string `parquet:"source_id"`
	Release    string `parquet:"release"`
	Priority   int64  `parquet:"priority"`
	Attributes string `parquet:"attributes"`
	Paths      string `parquet:"paths"`
	Raw        string `parquet:"raw"`
}

type provenanceRow struct {
	EntityID   string `parquet:"entity_id"`
	Attribute  string `parquet:"attribute"`
	SourceKey  string `parquet:"source_key"`
	SourcePath string `parquet:"source_path"`
}

type relationshipRow struct {
	FromID   string `parquet:"from_id"`
	ToID     string `parquet:"to_id"`
	Kind     string `parquet:"kind"`
	Evidence string `parquet:"evidence"`
}

type metadataRow struct {
	Key   string `parquet:"key"`
	Value string `parquet:"value"`
}
