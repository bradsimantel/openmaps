package importer

import (
	"encoding/json"
	"testing"

	"openmaps/internal/places"
)

func TestAreaRankingEvidence(t *testing.T) {
	evidence, err := AreaRankingEvidence("overture:division", json.RawMessage(`{
  "properties":{"class":"city","cartography":{"prominence":83}}
}`))
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Prominence != 83 || evidence.SettlementTier != places.SettlementCity {
		t.Fatalf("evidence: %+v", evidence)
	}
	evidence, err = AreaRankingEvidence("fixture:division", json.RawMessage(`{"anything":true}`))
	if err != nil || evidence != (places.AreaRankingEvidence{}) {
		t.Fatalf("unknown provider evidence: %+v err=%v", evidence, err)
	}
	if _, err = AreaRankingEvidence("overture:division", json.RawMessage(`{
  "properties":{"cartography":{"prominence":101}}
}`)); err == nil {
		t.Fatal("out-of-range prominence accepted")
	}
}
