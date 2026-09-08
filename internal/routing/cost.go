package routing

import "math"

// CostModel versions elapsed-time semantics independently of graph and access.
const CostModel = "estimated-driving-v1"

// Speed is an effective driving estimate, distinct from an interpreted numeric
// legal ceiling. Notes preserve assumptions/uncertainty, not confidence scores.
type Speed struct {
	KPH      float64  `json:"kph"`
	LimitKPH float64  `json:"limit_kph,omitempty"`
	Notes    []string `json:"notes"`
}

// ApplyLegalCeiling keeps legal bounds distinct from effective movement speed.
// The headroom factor is a cost-model assumption, not provider unit parsing.
func (s *Speed) ApplyLegalCeiling(kph float64) {
	if s.LimitKPH == 0 || kph < s.LimitKPH {
		s.LimitKPH = kph
	}
	s.KPH = math.Min(s.KPH, .8*kph)
}

type WayCost struct {
	Way      int64 `json:"way"`
	Forward  Speed `json:"forward"`
	Backward Speed `json:"backward"`
}

// DefaultSpeed is an uncalibrated, conservative effective speed in km/h. Lower
// speeds on local/service roads allow for maneuvering and ordinary interruptions.
// No separate junction delays or non-time preference penalties are added.
func DefaultSpeed(highway, service, surface, junction string) (float64, []string) {
	v := map[string]float64{"motorway": 80, "trunk": 65, "primary": 40, "secondary": 35, "tertiary": 30, "residential": 25, "unclassified": 25, "living_street": 7, "service": 10, "motorway_link": 40, "trunk_link": 35, "primary_link": 30, "secondary_link": 25, "tertiary_link": 25}[highway]
	if v == 0 {
		v = 10
	}
	notes := []string{"uncalibrated class default"}
	if highway == "service" {
		switch service {
		case "driveway", "parking_aisle", "drive-through", "slipway":
			v = 7
		case "", "alley":
		default:
			v = 7
			notes = append(notes, "unknown/special service classification")
		}
	}
	cap := v
	switch surface {
	case "":
		notes = append(notes, "surface absent")
	case "asphalt", "paved", "concrete", "concrete:lanes", "concrete:plates":
	case "paving_stones", "sett", "brick", "metal", "compacted", "fine_gravel":
		cap = 20
	case "unpaved", "gravel", "ground", "dirt", "cobblestone", "unhewn_cobblestone", "pebblestone":
		cap = 15
	case "grass", "sand", "dirt/sand":
		cap = 7
	default:
		cap = 10
		notes = append(notes, "unknown surface")
	}
	if cap < v {
		v = cap
		notes = append(notes, "surface cap")
	}
	if (junction == "roundabout" || junction == "circular") && v > 20 {
		v = 20
		notes = append(notes, "circulatory-road cap")
	}
	return v, notes
}
