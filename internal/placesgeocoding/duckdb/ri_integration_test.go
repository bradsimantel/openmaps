//go:build integration

package duckdb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRhodeIslandServingIndex(t *testing.T) {
	entitiesPath := os.Getenv("OPENMAPS_RI_ENTITIES")
	if entitiesPath == "" {
		t.Fatal("set absolute OPENMAPS_RI_ENTITIES")
	}
	indexPath := filepath.Join(t.TempDir(), IndexName)
	if retained := os.Getenv("OPENMAPS_RI_INDEX"); retained != "" {
		if !filepath.IsAbs(retained) {
			t.Fatal("OPENMAPS_RI_INDEX must be absolute")
		}
		if _, err := os.Stat(retained); !os.IsNotExist(err) {
			t.Fatalf("OPENMAPS_RI_INDEX must not exist: %v", err)
		}
		indexPath = retained
	}
	started := time.Now()
	manifest := json.RawMessage(`{"bbox":[-71.862,41.146,-71.12,42.019]}`)
	memoryLimit := os.Getenv("OPENMAPS_RI_MEMORY_LIMIT")
	if memoryLimit == "" {
		memoryLimit = "256MB"
	}
	if err := buildIndexWithMemoryLimit(context.Background(), indexPath, entitiesPath, 704693, manifest, map[string]string{}, memoryLimit); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("build memory_limit=%s elapsed=%s bytes=%d", memoryLimit, time.Since(started), stat.Size())
	db, err := openDatabase(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{db: db}
	expected := map[string][]string{
		"ma": {
			"om_3c1c110fa77b6a0e92014a4dcfc3622f", "om_acdaa3227553afb291be2ded9de93ff8",
			"om_babd68fbcf315e077d18b4a1c573314a", "om_f4e9f848548557bb796ddbf5d3f1920c",
			"om_f7a6c0318f97f08179d68eb05b77fb05",
		},
		"white horse": {
			"om_0e043cde0e8f1460e2cb49b29dcfb66b", "om_423482852ae4a1f2881dc6bc0434bdc6",
			"om_c4be7bb75b11b3b7744800eac70f34e4", "om_a5e3dc7692e4d3b90b71b94fba66ec5b",
			"om_f36fdcdf23f3c1271bf224f386451543",
		},
		"main street": {
			"om_0396e11ae3da5a0351866e478d443c64", "om_2758afd364ba3e1038093c316fbe99c8",
			"om_6273196e4f21bf35a607d1f1f3f7137d", "om_3a22cbb4c6fd845d62863a761e8b462e",
			"om_4a44e1e4f5b4ac1f817707230a9f7e48",
		},
		"providence": {
			"om_4ee77bf2e7eb8c5e1bbc573b59e1cf1b", "om_023e4d0470225cc62a8d8eafccc80099",
			"om_974590dedee1a681d5b4542443ebc18e", "om_2e766fcae82b00569cd7ebb8e9cd8cce",
			"om_6793dd19b8267f6c4bc8b179472c79ac",
		},
		"50 bellevue": {
			"om_293e1bbd8bde08ff291d27098045a8ad", "om_5ae67cfb6fb7827f16b3ca7a3c4695fd",
			"om_6b272c4f456f93b742c147ca623feb8c", "om_b4f1d22c6618cf00f0b59d2c5a9a2306",
			"om_f88c096070879478ee02e036d3e67480",
		},
		"dunkin": {
			"om_551c6f5e6f2c30a248ac5a711c7a6d7c", "om_21e39307667752fc4edec85873ec4cdf",
			"om_6b2facc03a3808af6775184ab490cc90", "om_8d9a4ffe95b4838903c30597cd4ec447",
			"om_a36cc91fe74c01804ae2d8416056ebe3",
		},
		"rhode island": {
			"om_ac9780d8e0664fc61c40a4e48a3b407a", "om_1a489c2eebdab351c5a5f4fc48cdb59b",
			"om_00d5e0bd624360ca496497d3a111c19e", "om_0f685a7e70aef32c5dab813db4dc94c2",
			"om_2b5002f97e66bf177c1b84bff02d63db",
		},
		"zzzzz missing": {},
	}
	for query, want := range expected {
		got, queryErr := store.Autocomplete(context.Background(), query)
		if queryErr != nil {
			t.Fatal(query, queryErr)
		}
		ids := make([]string, len(got))
		for i := range got {
			ids[i] = got[i].ID
		}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("%q ids differ\ngot:  %v\nwant: %v", query, ids, want)
		}
	}
	var rankTables int
	if err = db.QueryRow(`SELECT count(*) FROM information_schema.tables
WHERE table_name IN ('rank_entities','entity_ranks')`).Scan(&rankTables); err != nil || rankTables != 0 {
		t.Fatalf("serving catalog retained a complete rank table: count=%d err=%v", rankTables, err)
	}
	planRows, err := db.Query(`EXPLAIN ANALYZE SELECT p.entity_seq
FROM tokens t JOIN postings p USING(token_id)
WHERE t.token>='main' AND t.token<'maio'`)
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for planRows.Next() {
		var key, value string
		if err = planRows.Scan(&key, &value); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(value)
	}
	planRows.Close()
	planText := strings.ToLower(plan.String())
	if !strings.Contains(planText, "postings") || !strings.Contains(planText, "token") || !strings.Contains(planText, "main") || !strings.Contains(planText, "maio") {
		t.Fatalf("token-prefix plan lost its selective sorted range:\n%s", plan.String())
	}

	queries := []string{"ma", "white horse", "main street", "providence", "50 bellevue", "dunkin", "rhode island", "zzzzz missing"}
	measure := func(workers int) (p50, p95, p99 time.Duration) {
		durations := make([]time.Duration, 100)
		jobs := make(chan int)
		var wg sync.WaitGroup
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					start := time.Now()
					if _, queryErr := store.Autocomplete(context.Background(), queries[i%len(queries)]); queryErr != nil {
						t.Error(queryErr)
					}
					durations[i] = time.Since(start)
				}
			}()
		}
		for i := range durations {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		return durations[49], durations[94], durations[98]
	}
	p50, p95, p99 := measure(1)
	t.Logf("autocomplete-1 p50=%s p95=%s p99=%s", p50, p95, p99)
	p50, p95, p99 = measure(4)
	t.Logf("autocomplete-4 p50=%s p95=%s p99=%s", p50, p95, p99)
}
