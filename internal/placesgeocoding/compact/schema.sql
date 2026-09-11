PRAGMA foreign_keys=ON;
CREATE TABLE metadata(key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE entity_locator(
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind IN ('business','address','street','area')),
 entity_row_group INTEGER NOT NULL CHECK(entity_row_group >= 0),
 entity_row INTEGER NOT NULL CHECK(entity_row >= 0),
 source_start INTEGER NOT NULL CHECK(source_start >= 0),
 source_count INTEGER NOT NULL CHECK(source_count > 0),
 provenance_start INTEGER NOT NULL CHECK(provenance_start >= 0),
 provenance_count INTEGER NOT NULL CHECK(provenance_count > 0)
);
CREATE TABLE search_entities(
 rowid INTEGER PRIMARY KEY,
 id TEXT NOT NULL UNIQUE REFERENCES entity_locator(id),
 kind TEXT NOT NULL,
 name TEXT NOT NULL,
 normalized_name TEXT NOT NULL,
 address TEXT NOT NULL,
 subtype TEXT NOT NULL,
 closed INTEGER NOT NULL
);
CREATE VIRTUAL TABLE entity_fts USING fts5(
 name,address,aliases,content='',tokenize='unicode61 remove_diacritics 2',prefix='2 3 4'
);
CREATE TABLE short_prefix_head(
 prefix TEXT NOT NULL,
 rank INTEGER NOT NULL CHECK(rank BETWEEN 0 AND 4),
 entity_id TEXT NOT NULL REFERENCES search_entities(id),
 PRIMARY KEY(prefix,rank),
 UNIQUE(prefix,entity_id)
);
CREATE TABLE address_lookup(
 rowid INTEGER PRIMARY KEY,
 entity_id TEXT NOT NULL UNIQUE REFERENCES entity_locator(id),
 address_key TEXT NOT NULL,
 context TEXT NOT NULL,
 lat REAL NOT NULL,
 lng REAL NOT NULL
);
CREATE INDEX address_key ON address_lookup(address_key,entity_id);
CREATE VIRTUAL TABLE address_rtree USING rtree(
 rowid,min_lng,max_lng,min_lat,max_lat
);
INSERT INTO metadata VALUES('schema_version','1');
