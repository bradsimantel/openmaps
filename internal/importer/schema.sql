PRAGMA foreign_keys=ON;
CREATE TABLE metadata(key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE entities(
 id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK(kind IN ('business','address','street','area')),
 name TEXT NOT NULL, normalized_name TEXT NOT NULL, address TEXT NOT NULL,
 website TEXT NOT NULL, subtype TEXT NOT NULL, lat REAL NOT NULL CHECK(lat BETWEEN -90 AND 90),
 lng REAL NOT NULL CHECK(lng BETWEEN -180 AND 180), closed INTEGER NOT NULL,
 attributions TEXT NOT NULL
);
CREATE TABLE source_records(
 source_key TEXT PRIMARY KEY, entity_id TEXT NOT NULL REFERENCES entities(id),
 source TEXT NOT NULL, source_id TEXT NOT NULL, release TEXT NOT NULL,
 priority INTEGER NOT NULL, attributes TEXT NOT NULL, paths TEXT NOT NULL, raw TEXT NOT NULL,
 UNIQUE(source,source_id), UNIQUE(entity_id,source_key)
);
CREATE TABLE attribute_provenance(
 entity_id TEXT NOT NULL, attribute TEXT NOT NULL, source_key TEXT NOT NULL, source_path TEXT NOT NULL,
 PRIMARY KEY(entity_id,attribute),
 FOREIGN KEY(entity_id,source_key) REFERENCES source_records(entity_id,source_key)
);
CREATE TABLE relationships(
 from_id TEXT NOT NULL REFERENCES entities(id), to_id TEXT NOT NULL REFERENCES entities(id),
 kind TEXT NOT NULL CHECK(kind IN ('address','parent_area')), evidence TEXT NOT NULL,
 PRIMARY KEY(from_id,to_id,kind)
);
CREATE VIRTUAL TABLE entity_fts USING fts5(name,address,aliases,tokenize='unicode61 remove_diacritics 2',prefix='2 3 4');
INSERT INTO metadata VALUES('schema_version','1');
