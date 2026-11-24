// Package bloemgeo resolves a public IP address to a rough location with a
// local MaxMind GeoLite2-City database, for stream insights. It makes no
// network calls. Without a database every lookup is nil and the rest of
// stream insights works unchanged.
package bloemgeo

import (
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"sync"

	"github.com/oschwald/geoip2-golang"
)

// EnvCityDB names the environment variable holding the GeoLite2-City path.
const EnvCityDB = "BLOEM_GEOIP_CITY_DB"

// approximateRadiusKm is the accuracy radius above which a fix is shown as
// approximate, the threshold Proxy Manager uses for clients.
const approximateRadiusKm = 250

// Location is where an address probably is.
type Location struct {
	Country          string
	CountryCode      string
	City             string
	Latitude         float64
	Longitude        float64
	AccuracyRadiusKm int
	// Approximate is true for a country-level fix, whose coordinates are the
	// country's centroid, and for a fix wider than approximateRadiusKm.
	Approximate bool
}

// Resolver looks addresses up in one GeoLite2-City database.
type Resolver struct {
	mu sync.RWMutex
	db *geoip2.Reader
}

// Open loads the database at path.
func Open(path string) (*Resolver, error) {
	db, err := geoip2.Open(path)
	if err != nil {
		return nil, err
	}
	return &Resolver{db: db}, nil
}

// OpenFromEnv opens the database EnvCityDB names. It returns nil, with one
// Info log, when the variable is unset or the file cannot be opened.
func OpenFromEnv(logger *slog.Logger) *Resolver {
	if logger == nil {
		logger = slog.Default()
	}
	path := strings.TrimSpace(os.Getenv(EnvCityDB))
	if path == "" {
		logger.Info("stream insights locations off: no GeoLite2-City database configured", "env", EnvCityDB)
		return nil
	}
	resolver, err := Open(path)
	if err != nil {
		logger.Info("stream insights locations off: GeoLite2-City database unreadable", "env", EnvCityDB, "error", err)
		return nil
	}
	return resolver
}

// Available reports whether lookups can return locations.
func (r *Resolver) Available() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.db != nil
}

// Close releases the database.
func (r *Resolver) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return nil
	}
	err := r.db.Close()
	r.db = nil
	return err
}

// Lookup returns the location of a public address, or nil for an invalid,
// private, loopback, link-local or unspecified address, one the database
// does not know, or a resolver without a database.
func (r *Resolver) Lookup(ip string) *Location {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return nil
	}
	addr = addr.Unmap()
	if addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsUnspecified() || addr.IsMulticast() {
		return nil
	}
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.db == nil {
		return nil
	}
	record, err := r.db.City(addr.AsSlice())
	if err != nil {
		return nil
	}
	return locationFromCity(record)
}

// locationFromCity maps a GeoLite2-City record to a Location, or nil when it
// names neither a country nor a city.
func locationFromCity(record *geoip2.City) *Location {
	if record == nil {
		return nil
	}
	city := record.City.Names["en"]
	country := record.Country.Names["en"]
	if city == "" && country == "" {
		return nil
	}
	radius := int(record.Location.AccuracyRadius)
	hasCity := record.City.GeoNameID != 0 || city != ""
	return &Location{
		Country:          country,
		CountryCode:      record.Country.IsoCode,
		City:             city,
		Latitude:         record.Location.Latitude,
		Longitude:        record.Location.Longitude,
		AccuracyRadiusKm: radius,
		Approximate:      !hasCity || radius > approximateRadiusKm,
	}
}
