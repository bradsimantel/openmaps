package duckdb

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"
)

func TestLexicalPartitionsPreserveExactGlobalOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partitions.duckdb")
	db, err := sql.Open("duckdb", path+"?threads=2&memory_limit=64MB&temp_directory="+url.QueryEscape(filepath.Join(t.TempDir(), "spill")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE input_rejections(source_key VARCHAR,reason VARCHAR,raw VARCHAR);
CREATE TABLE input_identities(source_key VARCHAR,target VARCHAR);
INSERT INTO input_rejections VALUES
('ab2','z','{}'),('a','z','{}'),('ab1','z','{}'),('aa','z','{}'),('b','z','{}'),('ba','z','{}');
INSERT INTO input_identities VALUES ('ab2','2'),('a','0'),('ab1','1'),('aa','a'),('b','b'),('ba','ba')`); err != nil {
		t.Fatal(err)
	}
	partitions, err := planLexicalPartitions(context.Background(), db, "input_rejections", "source_key", 2)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, partition := range partitions {
		if partition.rows > 2 {
			t.Fatalf("oversized partition: %+v", partition)
		}
		rows, queryErr := queryLexicalPartition(context.Background(), db, "input_rejections", "source_key", "source_key", "source_key", partition)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		for rows.Next() {
			var key string
			if queryErr = rows.Scan(&key); queryErr != nil {
				t.Fatal(queryErr)
			}
			got = append(got, key)
		}
		if queryErr = rows.Close(); queryErr != nil {
			t.Fatal(queryErr)
		}
	}
	want := []string{"a", "aa", "ab1", "ab2", "b", "ba"}
	if !slices.Equal(got, want) {
		t.Fatalf("partitioned order=%q want %q", got, want)
	}
	identities, err := canonicalIdentities(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"a":"0","aa":"a","ab1":"1","ab2":"2","b":"b","ba":"ba"}`; identities != want {
		t.Fatalf("identities=%s want %s", identities, want)
	}
}

func TestNationalCardinalityBlockingOperators(t *testing.T) {
	if os.Getenv("OPENMAPS_NATIONAL_NORMALIZATION_STRESS") != "1" {
		t.Skip("set OPENMAPS_NATIONAL_NORMALIZATION_STRESS=1 for the exact-cardinality stress test")
	}
	const relationshipRows int64 = 3_703_480
	const rejectionRows int64 = 63_610_167
	root := t.TempDir()
	db, err := sql.Open("duckdb", filepath.Join(root, "stress.duckdb")+"?threads=4&memory_limit=8GB&preserve_insertion_order=false&temp_directory="+url.QueryEscape(filepath.Join(root, "spill")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(fmt.Sprintf(`CREATE TABLE input_relationships AS
SELECT 'from:'||i::VARCHAR AS from_key,'to:'||i::VARCHAR AS to_key,
       CASE WHEN i%%2=0 THEN 'address' ELSE 'parent_area' END AS kind,
       'stress' AS evidence
FROM range(%d) rows(i);
CREATE TABLE relationship_keys AS
SELECT source_key,entity_id,kind,CAST(hash(source_key)%%16 AS UTINYINT) AS source_bucket,
       substr(entity_id,4,1) AS entity_bucket
FROM (
  SELECT 'from:'||i::VARCHAR AS source_key,
         'om_'||substr(sha256('from:'||i::VARCHAR),1,32) AS entity_id,
         CASE WHEN i%%2=0 THEN 'business' ELSE 'area' END AS kind
  FROM range(%d) rows(i)
  UNION ALL
  SELECT 'to:'||i::VARCHAR AS source_key,
         'om_'||substr(sha256('to:'||i::VARCHAR),1,32) AS entity_id,
         CASE WHEN i%%2=0 THEN 'address' ELSE 'area' END AS kind
  FROM range(%d) rows(i)
);
CREATE TABLE input_rejections AS
SELECT 'overture:address:'||lpad(to_hex(i),16,'0') AS source_key,
       'outside_configured_scope' AS reason,'{}' AS raw
FROM range(%d) rows(i)`, relationshipRows, relationshipRows, relationshipRows, rejectionRows)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE relationship_from(
from_id VARCHAR,to_key VARCHAR,kind VARCHAR,evidence VARCHAR,from_kind VARCHAR,
from_entity_bucket VARCHAR,to_source_bucket UTINYINT)`); err != nil {
		t.Fatal(err)
	}
	for bucket := range 16 {
		if _, err = db.Exec(`INSERT INTO relationship_from
SELECT f.entity_id,r.to_key,r.kind,r.evidence,f.kind,f.entity_bucket,
       CAST(hash(r.to_key)%16 AS UTINYINT)
FROM input_relationships r
JOIN relationship_keys f ON f.source_key=r.from_key AND f.source_bucket=?
WHERE hash(r.from_key)%16=?`, bucket, bucket); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`CREATE TABLE resolved_relationships(
from_id VARCHAR,to_id VARCHAR,kind VARCHAR,evidence VARCHAR,from_kind VARCHAR,to_kind VARCHAR,
from_entity_bucket VARCHAR)`); err != nil {
		t.Fatal(err)
	}
	for bucket := range 16 {
		if _, err = db.Exec(`INSERT INTO resolved_relationships
SELECT r.from_id,t.entity_id,r.kind,r.evidence,r.from_kind,t.kind,r.from_entity_bucket
FROM relationship_from r
JOIN relationship_keys t ON t.source_key=r.to_key AND t.source_bucket=?
WHERE r.to_source_bucket=?`, bucket, bucket); err != nil {
			t.Fatal(err)
		}
	}
	var relationships int64
	for _, bucket := range normalizationEntityBuckets {
		rows, queryErr := db.Query(`SELECT from_id,to_id,kind,min(evidence),min(from_kind),min(to_kind)
FROM resolved_relationships WHERE from_entity_bucket=?
GROUP BY from_id,to_id,kind ORDER BY 1,2,3,4`, string(bucket))
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		for rows.Next() {
			var values [6]string
			if queryErr = rows.Scan(&values[0], &values[1], &values[2], &values[3], &values[4], &values[5]); queryErr != nil {
				t.Fatal(queryErr)
			}
			relationships++
		}
		if queryErr = rows.Close(); queryErr != nil {
			t.Fatal(queryErr)
		}
	}
	if relationships != relationshipRows {
		t.Fatalf("relationships=%d want %d", relationships, relationshipRows)
	}
	partitions, err := planLexicalPartitions(context.Background(), db, "input_rejections", "source_key", normalizationPartitionRows)
	if err != nil {
		t.Fatal(err)
	}
	var rejections int64
	for _, partition := range partitions {
		if partition.rows > normalizationPartitionRows {
			t.Fatalf("oversized rejection partition: %+v", partition)
		}
		rows, queryErr := queryLexicalPartition(context.Background(), db, "input_rejections", "source_key", "source_key,reason,raw", "source_key,reason", partition)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		for rows.Next() {
			var source, reason, raw string
			if queryErr = rows.Scan(&source, &reason, &raw); queryErr != nil {
				t.Fatal(queryErr)
			}
			rejections++
		}
		if queryErr = rows.Close(); queryErr != nil {
			t.Fatal(queryErr)
		}
	}
	if rejections != rejectionRows {
		t.Fatalf("rejections=%d want %d", rejections, rejectionRows)
	}
}
