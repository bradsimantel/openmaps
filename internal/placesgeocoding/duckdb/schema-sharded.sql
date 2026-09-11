CREATE TABLE metadata(key VARCHAR, value VARCHAR NOT NULL);

CREATE TABLE entity_locator AS
SELECT id,kind,regexp_extract(filename,'[^/\\\\]+$') AS entity_file,
       entity_row_group,entity_row,source_file,source_start,source_count,
       provenance_file,provenance_start,provenance_count
FROM read_parquet(?,filename=true) ORDER BY id;

CREATE TABLE search_entities AS
SELECT row_number() OVER (ORDER BY id)::INTEGER AS entity_seq,
       id,kind,name,normalized_name,address,normalized_address,
       normalized_aliases,subtype,closed
FROM read_parquet(?) ORDER BY id;

CREATE TABLE names AS
SELECT row_number() OVER (ORDER BY normalized_name)::INTEGER AS name_id,
       normalized_name
FROM (SELECT DISTINCT normalized_name FROM search_entities ORDER BY normalized_name);

CREATE TEMP TABLE entity_ranks AS
SELECT e.entity_seq,n.name_id,e.kind,e.closed,
       (CASE WHEN e.normalized_name='' THEN 0 ELSE len(string_split(e.normalized_name,' ')) END +
        CASE WHEN e.normalized_aliases='' THEN 0 ELSE len(string_split(e.normalized_aliases,' ')) END +
        CASE WHEN e.normalized_address='' THEN 0 ELSE len(string_split(e.normalized_address,' ')) END)::USMALLINT AS doc_len
FROM search_entities e JOIN names n USING(normalized_name)
ORDER BY e.entity_seq;

CREATE TABLE corpus_stats AS
SELECT count(*)::BIGINT AS n_docs,avg(doc_len)::DOUBLE AS avg_doc_len
FROM entity_ranks;

CREATE TABLE tokens AS
WITH fields AS (
  SELECT unnest(string_split(normalized_name,' ')) AS token FROM search_entities
  UNION ALL
  SELECT unnest(string_split(normalized_aliases,' ')) FROM search_entities WHERE normalized_aliases<>''
  UNION ALL
  SELECT unnest(string_split(normalized_address,' ')) FROM search_entities WHERE normalized_address<>''
)
SELECT row_number() OVER (ORDER BY token)::INTEGER AS token_id,token
FROM (SELECT DISTINCT token FROM fields WHERE length(token)>=1 ORDER BY token);

CREATE TEMP TABLE postings_stage(
  token_id INTEGER NOT NULL,
  entity_seq INTEGER NOT NULL,
  name_id INTEGER NOT NULL,
  kind VARCHAR NOT NULL,
  closed BOOLEAN NOT NULL,
  doc_len USMALLINT NOT NULL,
  name_tf USMALLINT NOT NULL,
  alias_tf USMALLINT NOT NULL,
  address_tf USMALLINT NOT NULL
);

CREATE TABLE short_prefix_head(
  prefix VARCHAR NOT NULL,
  rank UTINYINT NOT NULL,
  entity_seq INTEGER NOT NULL
);

CREATE TABLE address_lookup AS
SELECT s.entity_seq,l.id AS entity_id,e.address_key,e.address_context AS context,
       e.lat,e.lng
FROM read_parquet(?) e
JOIN entity_locator l USING(id)
JOIN search_entities s USING(id)
WHERE l.kind='address' AND e.address_key<>''
ORDER BY e.address_key,l.id;

CREATE TABLE address_spatial AS
SELECT *,floor((lat+90.0)*1000.0)::BIGINT AS lat_cell,
       floor((lng+180.0)*1000.0)::BIGINT AS lng_cell,
       (lat_cell*360001+lng_cell) AS cell_id
FROM address_lookup ORDER BY cell_id,entity_id;
