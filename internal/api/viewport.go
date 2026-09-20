package api

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"openmaps/internal/places"
)

func parsePlacesLocationBias(raw json.RawMessage) (*places.Viewport, error) {
	var bias map[string]json.RawMessage
	if err := json.Unmarshal(raw, &bias); err != nil || bias == nil {
		return nil, fmt.Errorf("locationBias must be an object")
	}
	if len(bias) != 1 {
		return nil, fmt.Errorf("locationBias must contain exactly one rectangle")
	}
	rectangleRaw, ok := bias["rectangle"]
	if !ok {
		if _, circle := bias["circle"]; circle {
			return nil, fmt.Errorf("locationBias.circle is unsupported; use locationBias.rectangle")
		}
		for field := range bias {
			return nil, fmt.Errorf("unsupported locationBias field: %s", field)
		}
	}
	var rectangle map[string]json.RawMessage
	if err := json.Unmarshal(rectangleRaw, &rectangle); err != nil || rectangle == nil {
		return nil, fmt.Errorf("locationBias.rectangle must be an object")
	}
	if len(rectangle) != 2 {
		return nil, fmt.Errorf("locationBias.rectangle requires low and high")
	}
	lowRaw, lowOK := rectangle["low"]
	highRaw, highOK := rectangle["high"]
	if !lowOK || !highOK {
		return nil, fmt.Errorf("locationBias.rectangle requires low and high")
	}
	low, err := parsePlacesLatLng(lowRaw, "locationBias.rectangle.low")
	if err != nil {
		return nil, err
	}
	high, err := parsePlacesLatLng(highRaw, "locationBias.rectangle.high")
	if err != nil {
		return nil, err
	}
	viewport := places.Viewport{South: low.Lat, West: low.Lng, North: high.Lat, East: high.Lng}
	if err = viewport.Validate(); err != nil {
		return nil, fmt.Errorf("invalid locationBias.rectangle: %w", err)
	}
	return &viewport, nil
}

func parsePlacesLatLng(raw json.RawMessage, field string) (places.Location, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return places.Location{}, fmt.Errorf("%s must be an object", field)
	}
	if len(values) != 2 {
		return places.Location{}, fmt.Errorf("%s requires latitude and longitude", field)
	}
	latitude, latOK := values["latitude"]
	longitude, lngOK := values["longitude"]
	if !latOK || !lngOK {
		return places.Location{}, fmt.Errorf("%s requires latitude and longitude", field)
	}
	var point places.Location
	if err := json.Unmarshal(latitude, &point.Lat); err != nil {
		return places.Location{}, fmt.Errorf("%s.latitude must be a number", field)
	}
	if err := json.Unmarshal(longitude, &point.Lng); err != nil {
		return places.Location{}, fmt.Errorf("%s.longitude must be a number", field)
	}
	return point, nil
}

func parseGeocodingBounds(value string) (*places.Viewport, error) {
	corners := strings.Split(value, "|")
	if len(corners) != 2 {
		return nil, fmt.Errorf("bounds requires southwest_latitude,southwest_longitude|northeast_latitude,northeast_longitude")
	}
	parseCorner := func(value, name string) (places.Location, error) {
		parts := strings.Split(value, ",")
		if len(parts) != 2 {
			return places.Location{}, fmt.Errorf("bounds %s corner requires latitude,longitude", name)
		}
		lat, latErr := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		lng, lngErr := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if latErr != nil || lngErr != nil {
			return places.Location{}, fmt.Errorf("bounds %s corner requires numeric latitude,longitude", name)
		}
		return places.Location{Lat: lat, Lng: lng}, nil
	}
	low, err := parseCorner(corners[0], "southwest")
	if err != nil {
		return nil, err
	}
	high, err := parseCorner(corners[1], "northeast")
	if err != nil {
		return nil, err
	}
	viewport := places.Viewport{South: low.Lat, West: low.Lng, North: high.Lat, East: high.Lng}
	if err = viewport.Validate(); err != nil {
		return nil, fmt.Errorf("invalid bounds: %w", err)
	}
	return &viewport, nil
}
