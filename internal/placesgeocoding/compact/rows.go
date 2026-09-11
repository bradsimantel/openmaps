package compact

type entityRow struct {
	ID           string  `parquet:"id"`
	Kind         string  `parquet:"kind"`
	Name         string  `parquet:"name"`
	Address      string  `parquet:"address"`
	Website      string  `parquet:"website"`
	Subtype      string  `parquet:"subtype"`
	Lat          float64 `parquet:"lat"`
	Lng          float64 `parquet:"lng"`
	Closed       bool    `parquet:"closed"`
	Attributions string  `parquet:"attributions"`
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
