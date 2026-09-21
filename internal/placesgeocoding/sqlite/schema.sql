PRAGMA foreign_keys=ON;

CREATE TABLE metadata(
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
) WITHOUT ROWID;

CREATE TABLE entities(
  rowid INTEGER PRIMARY KEY,
  id TEXT NOT NULL UNIQUE,
  kind TEXT NOT NULL CHECK(kind IN ('business','address','street','area')),
  subtype TEXT NOT NULL,
  name TEXT NOT NULL,
  normalized_name TEXT NOT NULL,
  aliases TEXT NOT NULL,
  formatted_address TEXT NOT NULL,
  normalized_address TEXT NOT NULL,
  locality TEXT NOT NULL,
  region TEXT NOT NULL,
  region_code TEXT NOT NULL,
  country TEXT NOT NULL,
  postal_code TEXT NOT NULL,
  hierarchy TEXT NOT NULL,
  lat REAL NOT NULL CHECK(lat BETWEEN -90 AND 90),
  lng REAL NOT NULL CHECK(lng BETWEEN -180 AND 180),
  closed INTEGER NOT NULL CHECK(closed IN (0,1)),
  area_prominence INTEGER NOT NULL,
  settlement_tier INTEGER NOT NULL,
  destination_class INTEGER NOT NULL,
  specificity INTEGER NOT NULL,
  confidence_tier INTEGER NOT NULL,
  area_override INTEGER NOT NULL CHECK(area_override IN (0,1))
);

CREATE INDEX entities_exact_name
  ON entities(normalized_name,kind,closed,rowid);

CREATE VIRTUAL TABLE entity_fts USING fts5(
  name,
  aliases,
  address,
  hierarchy,
  content='',
  tokenize='unicode61 remove_diacritics 2',
  prefix='2 3 4',
  columnsize=1
);

CREATE VIRTUAL TABLE entity_rtree USING rtree(
  rowid,
  min_lng,max_lng,
  min_lat,max_lat
);

CREATE VIRTUAL TABLE entity_terms USING fts5vocab(entity_fts,'row');

CREATE TABLE short_prefix_head(
  prefix TEXT NOT NULL,
  rank INTEGER NOT NULL,
  entity_rowid INTEGER NOT NULL,
  PRIMARY KEY(prefix,rank),
  UNIQUE(prefix,entity_rowid)
) WITHOUT ROWID;

INSERT INTO metadata VALUES('schema_version','1');
