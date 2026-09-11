INSERT INTO short_prefix_head
WITH prefix_postings AS (
  SELECT left(t.token,1) AS prefix,p.* FROM tokens t JOIN postings p USING(token_id)
  UNION ALL
  SELECT left(t.token,2) AS prefix,p.* FROM tokens t JOIN postings p USING(token_id)
  WHERE length(t.token)>=2
), matches AS (
  SELECT prefix,entity_seq,any_value(name_id) AS name_id,
         any_value(kind) AS kind,any_value(closed) AS closed,
         any_value(doc_len) AS doc_len,
         sum(name_tf)::INTEGER AS name_tf,
         sum(alias_tf)::INTEGER AS alias_tf,
         sum(address_tf)::INTEGER AS address_tf
  FROM prefix_postings GROUP BY prefix,entity_seq
), frequencies AS (
  SELECT *,count(*) OVER (PARTITION BY prefix) AS df FROM matches
), scored AS (
  SELECT m.*,
    CASE WHEN n.normalized_name=m.prefix THEN 0 ELSE 1 END AS exact_rank,
    CASE WHEN starts_with(n.normalized_name,m.prefix) THEN 0 ELSE 1 END AS name_prefix_rank,
    CASE m.kind WHEN 'area' THEN 0 WHEN 'street' THEN 1 WHEN 'business' THEN 2 ELSE 3 END AS kind_rank,
    greatest(ln((s.n_docs-m.df+0.5)/(m.df+0.5)),0.000001)*
      (((10.0*m.name_tf+5.0*m.alias_tf+m.address_tf)*2.2)/
       ((10.0*m.name_tf+5.0*m.alias_tf+m.address_tf)+
        1.2*(0.25+0.75*m.doc_len/s.avg_doc_len))) AS bm25_score
  FROM frequencies m JOIN names n USING(name_id)
  CROSS JOIN corpus_stats s WHERE NOT m.closed
), street_collapsed AS (
  SELECT *,row_number() OVER (
    PARTITION BY prefix,(kind='street'),CASE WHEN kind='street' THEN name_id ELSE entity_seq END
    ORDER BY exact_rank,name_prefix_rank,kind_rank,bm25_score DESC,entity_seq) AS street_rank
  FROM scored
), ranked AS (
  SELECT *,row_number() OVER (
    PARTITION BY prefix
    ORDER BY exact_rank,name_prefix_rank,kind_rank,bm25_score DESC,entity_seq)-1 AS result_rank
  FROM street_collapsed WHERE street_rank=1
)
SELECT prefix,result_rank::UTINYINT,entity_seq
FROM ranked WHERE result_rank<5 ORDER BY prefix,result_rank;
