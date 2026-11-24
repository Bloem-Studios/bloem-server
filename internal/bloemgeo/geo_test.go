package bloemgeo

import (
	"testing"

	"github.com/oschwald/geoip2-golang"
)

func cityRecord(city, countryCode, country string, lat, lon float64, radius uint16) *geoip2.City {
	record := &geoip2.City{}
	if city != "" {
		record.City.GeoNameID = 2751773
		record.City.Names = map[string]string{"en": city}
	}
	record.Country.IsoCode = countryCode
	record.Country.Names = map[string]string{"en": country}
	record.Location.Latitude = lat
	record.Location.Longitude = lon
	record.Location.AccuracyRadius = radius
	return record
}

func TestLocationFromCity(t *testing.T) {
	got := locationFromCity(cityRecord("Leiden", "NL", "Netherlands", 52.16, 4.49, 20))
	if got == nil {
		t.Fatal("city record gave no location")
	}
	if got.City != "Leiden" || got.CountryCode != "NL" || got.Country != "Netherlands" {
		t.Fatalf("location = %+v, want Leiden, NL", got)
	}
	if got.Latitude != 52.16 || got.Longitude != 4.49 || got.AccuracyRadiusKm != 20 {
		t.Fatalf("coordinates = %+v, want 52.16/4.49 within 20 km", got)
	}
	if got.Approximate {
		t.Fatal("a city fix within 20 km is marked approximate")
	}
}

func TestLocationFromCityWideRadiusIsApproximate(t *testing.T) {
	got := locationFromCity(cityRecord("Leiden", "NL", "Netherlands", 52.16, 4.49, 300))
	if got == nil || !got.Approximate {
		t.Fatalf("location = %+v, want approximate for a 300 km radius", got)
	}
}

func TestLocationFromCountryOnlyIsApproximate(t *testing.T) {
	got := locationFromCity(cityRecord("", "NL", "Netherlands", 52.38, 4.9, 100))
	if got == nil {
		t.Fatal("country fix gave no location")
	}
	if !got.Approximate || got.City != "" || got.CountryCode != "NL" {
		t.Fatalf("location = %+v, want an approximate country-level fix", got)
	}
}

func TestLocationFromEmptyRecord(t *testing.T) {
	if got := locationFromCity(&geoip2.City{}); got != nil {
		t.Fatalf("empty record = %+v, want nil", got)
	}
	if got := locationFromCity(nil); got != nil {
		t.Fatalf("nil record = %+v, want nil", got)
	}
}

func TestLookupWithoutDatabase(t *testing.T) {
	var r *Resolver
	if got := r.Lookup("8.8.8.8"); got != nil {
		t.Fatalf("nil resolver lookup = %+v, want nil", got)
	}
	if r.Available() {
		t.Fatal("nil resolver reports a database")
	}
	if (&Resolver{}).Available() {
		t.Fatal("resolver without a database reports one")
	}
}

func TestLookupIgnoresPrivateAddresses(t *testing.T) {
	r := &Resolver{}
	for _, ip := range []string{"fd00::1", "fc00:1::5", "127.0.0.1", "::1", "169.254.1.1", "fe80::1", "not-an-ip", ""} {
		if got := r.Lookup(ip); got != nil {
			t.Fatalf("Lookup(%q) = %+v, want nil", ip, got)
		}
	}
}

func TestOpenFromEnvWithoutPath(t *testing.T) {
	t.Setenv(EnvCityDB, "")
	if r := OpenFromEnv(nil); r != nil {
		t.Fatalf("OpenFromEnv without a path = %+v, want nil", r)
	}
	t.Setenv(EnvCityDB, t.TempDir()+"/missing.mmdb")
	if r := OpenFromEnv(nil); r != nil {
		t.Fatalf("OpenFromEnv with a missing file = %+v, want nil", r)
	}
}
