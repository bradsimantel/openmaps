package importer

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"openmaps/internal/routing"
)

var speedNumber = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)(?: (mph|km/h|kph|knots))?$`)

// OSM unitless speeds are km/h even in US data. Never infer mph from geography.
func speedKPH(value string) (float64, bool) {
	m := speedNumber.FindStringSubmatch(strings.TrimSpace(value))
	if m == nil {
		return 0, false
	}
	v, e := strconv.ParseFloat(m[1], 64)
	if e != nil {
		return 0, false
	}
	switch m[2] {
	case "mph":
		v *= 1.609344
	case "knots":
		v *= 1.852
	}
	return v, e == nil && v > 0 && v <= 300 && !math.IsInf(v, 0)
}

// Split only outside parentheses; semicolons inside opening-hours conditions
// are not separate speed values. Conditions are retained, never evaluated.
func conditionalSpeeds(value string) ([]string, bool) {
	depth, start := 0, 0
	parts := []string{}
	for i, c := range value {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, false
			}
		case ';':
			if depth == 0 {
				parts = append(parts, value[start:i])
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, false
	}
	parts = append(parts, value[start:])
	out := []string{}
	for _, p := range parts {
		v, c, ok := strings.Cut(p, "@")
		if !ok || strings.Trim(strings.TrimSpace(c), "() ") == "" || strings.Contains(c, "@") {
			return nil, false
		}
		out = append(out, strings.TrimSpace(v))
	}
	return out, len(out) > 0
}

func drivingSpeed(tags map[string]string, direction string) routing.Speed {
	v, notes := routing.DefaultSpeed(tags["highway"], tags["service"], tags["surface"], tags["junction"])
	result := routing.Speed{KPH: v, Notes: notes}
	apply := func(value string, legal bool) {
		if n, ok := speedKPH(value); ok {
			if legal {
				result.ApplyLegalCeiling(n)
			} else {
				result.KPH = math.Min(result.KPH, n)
			}
			return
		}
		switch strings.TrimSpace(value) {
		case "none":
			result.Notes = append(result.Notes, "no numeric ceiling: none")
		case "walk":
			result.KPH = math.Min(result.KPH, 4)
			result.Notes = append(result.Notes, "walk: assumed 4 km/h, legal walking speed unspecified")
		default:
			result.KPH = math.Min(result.KPH, 5)
			result.Notes = append(result.Notes, "unresolved speed value: "+value)
		}
	}
	// More specific vehicle and directional tags override the generic base limit.
	selected := ""
	baseKeys := map[string]bool{}
	for _, base := range []string{"maxspeed", "maxspeed:vehicle", "maxspeed:motor_vehicle", "maxspeed:motorcar"} {
		baseKeys[base], baseKeys[base+":forward"], baseKeys[base+":backward"] = true, true, true
		if _, ok := tags[base]; ok {
			selected = base
		}
		if _, ok := tags[base+":"+direction]; ok {
			selected = base + ":" + direction
		}
	}
	if selected != "" {
		apply(tags[selected], true)
		result.Notes = append(result.Notes, "base limit: "+selected)
	} else {
		result.Notes = append(result.Notes, "legal maximum absent")
	}
	// Conditional/scoped constraints are deliberately intersected, not overridden:
	// without a departure time, use every potentially applicable lower ceiling.
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !strings.HasPrefix(k, "maxspeed:") {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(k, "maxspeed:"), ":")
		relevant, supported, conditional, advisory := true, true, false, false
		for _, p := range parts {
			switch p {
			case "hgv", "bus", "psv", "bicycle", "foot", "emergency", "motorcycle", "trailer", "hgv_articulated":
				relevant = false
			case "forward", "backward":
				if p != direction {
					relevant = false
				}
			case "vehicle", "motor_vehicle", "motorcar":
			case "conditional":
				conditional = true
			case "advisory":
				advisory = true
			case "type", "source", "signed", "reason", "practical":
				relevant = false // metadata or a different estimate, not a legal ceiling
			default:
				supported = false
			}
		}
		if !relevant {
			continue
		}
		if !supported || !conditional && !advisory && !baseKeys[k] {
			result.KPH = math.Min(result.KPH, 5)
			result.Notes = append(result.Notes, "unsupported speed key: "+k)
			continue
		}
		if conditional {
			values, ok := conditionalSpeeds(tags[k])
			if !ok {
				result.KPH = math.Min(result.KPH, 5)
				result.Notes = append(result.Notes, "malformed conditional: "+k)
				continue
			}
			for _, value := range values {
				apply(value, !advisory)
			}
			result.Notes = append(result.Notes, "all conditional ceilings applied: "+k)
		} else if advisory {
			apply(tags[k], false)
			result.Notes = append(result.Notes, "advisory cap: "+k)
		}
	}
	return result
}
