package addressdata

import (
	"openmaps/internal/places"
	"testing"
)

func TestComponents(t *testing.T) {
	for _, tc := range []struct {
		name, raw, label, formatted string
		want                        places.AddressComponents
	}{
		{"missing", `{"properties":{"number":"1","street":"A Street"}}`, "1 A Street", "1 A Street", places.AddressComponents{Number: "1", Street: "A Street"}},
		{"US locality", `{"properties":{"number":"1","street":"A Street","country":"US","address_levels":[{"value":"RI"},{"value":"Newport"}]}}`, "1 A Street", "1 A Street, RI, Newport, US", places.AddressComponents{Number: "1", Street: "A Street", Region: "RI", Locality: "Newport", Country: "US"}},
		{"foreign levels", `{"properties":{"number":"1","street":"A Street","country":"CA","address_levels":[{"value":"ON"},{"value":"Toronto"}]}}`, "1 A Street", "1 A Street, ON, Toronto, CA", places.AddressComponents{Number: "1", Street: "A Street", Country: "CA"}},
		{"conflicting name", `{"properties":{"number":"2","street":"A Street"}}`, "1 A Street", "2 A Street", places.AddressComponents{}},
		{"conflicting formatted", `{"properties":{"number":"1","street":"A Street"}}`, "1 A Street", "1 A Street, RI", places.AddressComponents{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Components("overture:address", tc.raw, tc.label, tc.formatted)
			if err != nil || got != tc.want {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
	if _, err := Components("overture:address", `{`, "", ""); err == nil {
		t.Fatal("malformed source accepted")
	}
	if got, err := Components("other", `{`, "", ""); err != nil || got != (places.AddressComponents{}) {
		t.Fatal(got, err)
	}
}
