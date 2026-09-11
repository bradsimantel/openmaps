package compact

import (
	"context"
	"database/sql"
	"strings"

	"openmaps/internal/places"
)

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// queryFTS is the correctness oracle used both for ordinary searches and to
// build the bounded two-character prefix heads. Caching only five final IDs per
// populated prefix avoids storing a national-size result projection.
func queryFTS(ctx context.Context, db queryer, normalized string) ([]places.Entity, error) {
	tokens := strings.Fields(normalized)
	for i, token := range tokens {
		tokens[i] = "\"" + token + "\"*"
	}
	rows, err := db.QueryContext(ctx, `SELECT e.id,e.kind,e.name,e.address,e.subtype FROM entity_fts
 JOIN search_entities e ON e.rowid=entity_fts.rowid WHERE entity_fts MATCH ? AND e.closed=0
 ORDER BY CASE WHEN e.normalized_name=? THEN 0 WHEN e.normalized_name LIKE ? THEN 1 ELSE 2 END,
 CASE WHEN e.kind='area' THEN 0 WHEN e.kind='street' THEN 1 WHEN e.kind='business' THEN 2 ELSE 3 END,
 bm25(entity_fts,10.0,1.0,5.0),e.id`, strings.Join(tokens, " AND "), normalized, normalized+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []places.Entity{}
	seenStreets := map[string]bool{}
	for rows.Next() {
		var entity places.Entity
		if err = rows.Scan(&entity.ID, &entity.Kind, &entity.Name, &entity.Address, &entity.Subtype); err != nil {
			return nil, err
		}
		if entity.Kind == "street" {
			key := places.Normalize(entity.Name)
			if seenStreets[key] {
				continue
			}
			seenStreets[key] = true
		}
		out = append(out, entity)
		if len(out) == 5 {
			break
		}
	}
	return out, rows.Err()
}
