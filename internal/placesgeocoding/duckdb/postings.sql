INSERT INTO postings_stage
WITH fields AS (
  SELECT entity_seq,4::UTINYINT AS field,
         unnest(string_split(normalized_name,' ')) AS token
  FROM search_entities WHERE entity_seq BETWEEN ? AND ?
  UNION ALL
  SELECT entity_seq,2::UTINYINT,
         unnest(string_split(normalized_aliases,' '))
  FROM search_entities
  WHERE entity_seq BETWEEN ? AND ? AND normalized_aliases<>''
  UNION ALL
  SELECT entity_seq,1::UTINYINT,
         unnest(string_split(normalized_address,' '))
  FROM search_entities
  WHERE entity_seq BETWEEN ? AND ? AND normalized_address<>''
), grouped AS (
  SELECT token,entity_seq,
         sum(CASE WHEN field=4 THEN 1 ELSE 0 END)::USMALLINT AS name_tf,
         sum(CASE WHEN field=2 THEN 1 ELSE 0 END)::USMALLINT AS alias_tf,
         sum(CASE WHEN field=1 THEN 1 ELSE 0 END)::USMALLINT AS address_tf
  FROM fields WHERE length(token)>=1 GROUP BY token,entity_seq
)
SELECT t.token_id,g.entity_seq,r.name_id,r.kind,r.closed,r.doc_len,
       g.name_tf,g.alias_tf,g.address_tf
FROM grouped g JOIN tokens t USING(token) JOIN entity_ranks r USING(entity_seq)
ORDER BY t.token_id,g.entity_seq;
