package importer

import (
	"math"
	"strconv"
	"testing"
)

func TestVehicleLimitUnits(t *testing.T) {
	for _, tc := range []struct {
		value string
		mass  bool
		want  float64
	}{
		{"1.9", false, 1.9}, {"2 m", false, 2}, {`6'3"`, false, 1.905}, {`14'9"`, false, 4.4958},
		{"1.8", true, 1.8}, {"1.8 t", true, 1.8}, {"1800 kg", true, 1.8}, {"2 st", true, 1.81436948}, {"4000 lbs", true, 1.81436948}, {"2 lt", true, 2.0320938176},
	} {
		v, ok := vehicleLimit(tc.value, tc.mass)
		if !ok || math.Abs(v-tc.want) > 1e-9 {
			t.Errorf("%q: %v %v", tc.value, v, ok)
		}
	}
	for _, v := range []string{"", "unknown", "0", "-1", "NaN", "Inf", "2,5", "2m", "2 meters", `8'0`, "26'", `6'12"`, "2;3", "2 @ (Mo)", "2 ton", "2 ft"} {
		for _, mass := range []bool{false, true} {
			if _, ok := vehicleLimit(v, mass); ok {
				t.Errorf("accepted malformed %q", v)
			}
		}
	}
	if _, ok := vehicleLimit("2 t", false); ok {
		t.Fatal("mass as height")
	}
	if _, ok := vehicleLimit("2 m", true); ok {
		t.Fatal("length as weight")
	}
}
func TestCarRestrictionThresholds(t *testing.T) {
	if !barrierBlocked(map[string]string{"barrier": "gate", "motorcar": "yes", "locked": "unknown"}) {
		t.Fatal("unknown lock ignored")
	}
	for k, threshold := range map[string]string{"maxheight": "1.9", "maxwidth": "2", "maxlength": "5", "maxweight": "1.8", "maxaxleload": "1.1"} {
		if !dimensionsAllowed(map[string]string{k: threshold}, "forward") {
			t.Fatal("equality blocked", k)
		}
		n, _ := strconv.ParseFloat(threshold, 64)
		if dimensionsAllowed(map[string]string{k: strconv.FormatFloat(n-.001, 'f', 3, 64)}, "forward") {
			t.Fatal("undersized restriction allowed", k)
		}
	}
	for _, tc := range []struct {
		tags map[string]string
		f, b bool
	}{
		{map[string]string{"maxheight": "1.5", "maxheight:forward": "2"}, true, false},
		{map[string]string{"maxheight": "5", "maxheight:physical": "1.5"}, false, false},
		{map[string]string{"maxweight": "4000 lbs"}, true, true},
		{map[string]string{"maxweight": "2 st"}, true, true},
		{map[string]string{"maxheight:conditional": "5 @ (Mo)"}, false, false},
		{map[string]string{"maxheight": "unknown"}, false, false},
		{map[string]string{"maxwidth:lanes": "2|3"}, false, false},
		{map[string]string{"maxweight:hgv:conditional": "1 @ (Mo)"}, true, true},
		{map[string]string{"maxheight:signed": "no"}, true, true},
	} {
		if dimensionsAllowed(tc.tags, "forward") != tc.f || dimensionsAllowed(tc.tags, "backward") != tc.b {
			t.Fatal(tc)
		}
	}
	if barrierBlocked(map[string]string{"barrier": "height_restrictor", "maxheight": `6'3"`}) {
		t.Fatal("compatible height barrier blocked")
	}
	if !barrierBlocked(map[string]string{"barrier": "height_restrictor", "maxheight": "1.8"}) {
		t.Fatal("low height barrier passed")
	}
	if !barrierBlocked(map[string]string{"maxweight": "1"}) {
		t.Fatal("node weight ignored")
	}
}
