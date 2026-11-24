package bloeminsights

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-server/internal/bloemgeo"
)

// Hop kinds, in route order.
const (
	HopViewer     = "viewer"
	HopCloudflare = "cloudflare"
	HopEdge       = "edge"
	HopServer     = "server"
	HopNode       = "node"
)

// Hop is one point on a stream's route.
type Hop struct {
	Kind        string  `json:"kind"`
	Label       string  `json:"label"`
	Latitude    float64 `json:"lat"`
	Longitude   float64 `json:"lon"`
	Approximate bool    `json:"approximate"`
}

// Positions are where the server and its transcode nodes are.
type Positions struct {
	Server *Hop
	Nodes  map[int]Hop
}

// SettingMapLocations is the server setting that places the server and its
// nodes on the map:
//
//	{"server":{"label":"Home","lat":52.1,"lon":5.1},"nodes":{"4":{"label":"a310","lat":51.9,"lon":4.4}}}
const SettingMapLocations = "bloem.map.locations"

// SettingsReader reads a server setting.
type SettingsReader interface {
	Get(ctx context.Context, key string) (string, error)
}

type mapPoint struct {
	Label string   `json:"label"`
	Lat   *float64 `json:"lat"`
	Lon   *float64 `json:"lon"`
}

func (p mapPoint) hop(kind string) (Hop, bool) {
	if p.Lat == nil || p.Lon == nil || *p.Lat < -90 || *p.Lat > 90 || *p.Lon < -180 || *p.Lon > 180 {
		return Hop{}, false
	}
	return Hop{Kind: kind, Label: strings.TrimSpace(p.Label), Latitude: *p.Lat, Longitude: *p.Lon}, true
}

// ServerPositions reads SettingMapLocations. With no server entry it places
// the server at the location of publicIP's address (the public IPv4 the
// server observed), when geo can resolve it. Entries out of range are left
// out; with neither, the server hop is nil.
func ServerPositions(ctx context.Context, settings SettingsReader, geo *bloemgeo.Resolver, publicIP func(context.Context) string) Positions {
	out := Positions{Nodes: map[int]Hop{}}
	if settings != nil {
		if raw, err := settings.Get(ctx, SettingMapLocations); err == nil && strings.TrimSpace(raw) != "" {
			var doc struct {
				Server *mapPoint           `json:"server"`
				Nodes  map[string]mapPoint `json:"nodes"`
			}
			if json.Unmarshal([]byte(raw), &doc) == nil {
				if doc.Server != nil {
					if hop, ok := doc.Server.hop(HopServer); ok {
						out.Server = &hop
					}
				}
				for key, point := range doc.Nodes {
					id, err := strconv.Atoi(key)
					if err != nil || id <= 0 {
						continue
					}
					if hop, ok := point.hop(HopNode); ok {
						out.Nodes[id] = hop
					}
				}
			}
		}
	}
	if out.Server == nil && publicIP != nil && geo.Available() {
		if location := geo.Lookup(publicIP(ctx)); location != nil {
			label := location.City
			if label == "" {
				label = location.Country
			}
			out.Server = &Hop{Kind: HopServer, Label: label, Latitude: location.Latitude, Longitude: location.Longitude, Approximate: location.Approximate}
		}
	}
	return out
}
