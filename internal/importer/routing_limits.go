package importer

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"openmaps/internal/routing"
)

var metricLimit = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)(?: (m|t|kg|st|lt|lbs))?$`)
var imperialLimit = regexp.MustCompile(`^([0-9]+)'([0-9]+(?:\.[0-9]+)?)"$`)

// OSM defaults are metres and metric tonnes, even for a US source. Do not
// repair ambiguous units, decimal commas, lists or incomplete feet/inches.
func vehicleLimit(value string, mass bool) (float64, bool) {
	if !mass {
		if m := imperialLimit.FindStringSubmatch(value); m != nil {
			feet, _ := strconv.ParseFloat(m[1], 64)
			inches, _ := strconv.ParseFloat(m[2], 64)
			v := feet*.3048 + inches*.0254
			return v, inches < 12 && v > 0 && !math.IsInf(v, 0)
		}
	}
	m := metricLimit.FindStringSubmatch(value)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	factor := 1.0
	if mass {
		switch m[2] {
		case "", "t":
		case "kg":
			factor = .001
		case "st":
			factor = .90718474
		case "lt":
			factor = 1.0160469088
		case "lbs":
			factor = .00045359237
		default:
			return 0, false
		}
	} else if m[2] != "" && m[2] != "m" {
		return 0, false
	}
	return v * factor, !math.IsInf(v*factor, 0)
}

// Direction-specific values override the base at the same scope. Physical
// clearances and legal limits both apply. Unsupported applicable suffixes close
// the feature; metadata and explicitly unrelated vehicle modes do not.
func dimensionsAllowed(t map[string]string, direction string) bool {
	for _, limit := range []struct {
		key   string
		value float64
		mass  bool
	}{
		{"maxheight", routing.CarHeight, false}, {"maxwidth", routing.CarWidth, false},
		{"maxlength", routing.CarLength, false}, {"maxweight", routing.CarWeight, true}, {"maxaxleload", routing.CarAxleLoad, true},
	} {
		for k := range t {
			if !strings.HasPrefix(k, limit.key+":") {
				continue
			}
			suffix := strings.TrimPrefix(k, limit.key+":")
			switch suffix {
			case "forward", "backward", "physical", "physical:forward", "physical:backward", "signed":
				continue
			}
			first := strings.Split(suffix, ":")[0]
			switch first {
			case "hgv", "hgv_articulated", "bus", "psv", "emergency":
				continue
			}
			// Car/motor-vehicle scopes are checked below, including directional forms.
			switch suffix {
			case "motorcar", "motorcar:forward", "motorcar:backward", "motor_vehicle", "motor_vehicle:forward", "motor_vehicle:backward", "vehicle", "vehicle:forward", "vehicle:backward":
				continue
			}
			return false
		}
		for _, suffix := range []string{"", ":physical", ":vehicle", ":motor_vehicle", ":motorcar"} {
			v, present := t[limit.key+suffix]
			if directional, ok := t[limit.key+suffix+":"+direction]; ok {
				v, present = directional, true
			}
			if !present {
				continue
			}
			if v == "none" || v == "default" {
				continue
			}
			n, ok := vehicleLimit(v, limit.mass)
			if !ok || n+1e-9 < limit.value {
				return false
			}
		}
	}
	return true
}
