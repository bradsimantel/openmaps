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

func TestPlaceRankingEvidence(t *testing.T) {
	evidence, err := PlaceRankingEvidence("overture:place", json.RawMessage(`{
  "properties":{
    "basic_category":"monument",
    "confidence":0.97,
    "taxonomy":{"hierarchy":["cultural_and_historic","historic_site","monument"],"primary":"monument"}
  }
}`))
	if err != nil {
		t.Fatal(err)
	}
	if evidence.DestinationClass != places.DestinationCultural || evidence.Specificity != 3 || evidence.ConfidenceTier != places.ConfidenceHigh {
		t.Fatalf("evidence: %+v", evidence)
	}
	evidence, err = PlaceRankingEvidence("overture:place", json.RawMessage(`{
  "properties":{"basic_category":"stadium_arena","confidence":0.8}
}`))
	if err != nil || evidence.DestinationClass != places.DestinationAttraction || evidence.Specificity != 1 || evidence.ConfidenceTier != places.ConfidenceMedium {
		t.Fatalf("basic-category fallback: %+v err=%v", evidence, err)
	}
	evidence, err = PlaceRankingEvidence("fixture:place", json.RawMessage(`{"anything":true}`))
	if err != nil || evidence != (places.PlaceRankingEvidence{}) {
		t.Fatalf("unknown provider evidence: %+v err=%v", evidence, err)
	}
	if _, err = PlaceRankingEvidence("overture:place", json.RawMessage(`{
  "properties":{"confidence":1.01}
}`)); err == nil {
		t.Fatal("out-of-range confidence accepted")
	}
}
