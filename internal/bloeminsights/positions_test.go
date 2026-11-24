package bloeminsights

import (
	"context"
	"errors"
	"testing"
)

type mapSettings map[string]string

func (m mapSettings) Get(_ context.Context, key string) (string, error) {
	if v, ok := m[key]; ok {
		return v, nil
	}
	return "", errors.New("not found")
}

func TestServerPositionsFromTheSetting(t *testing.T) {
	settings := mapSettings{SettingMapLocations: `{"server":{"label":"Home","lat":52.1,"lon":5.1},"nodes":{"4":{"label":"a310","lat":51.9,"lon":4.4},"x":{"label":"bad","lat":1,"lon":1},"5":{"label":"far","lat":95,"lon":1}}}`}
	got := ServerPositions(context.Background(), settings, nil, nil)
	if got.Server == nil || got.Server.Label != "Home" || got.Server.Kind != HopServer || got.Server.Latitude != 52.1 {
		t.Fatalf("server = %+v", got.Server)
	}
	if len(got.Nodes) != 1 || got.Nodes[4].Label != "a310" || got.Nodes[4].Kind != HopNode {
		t.Fatalf("nodes = %+v, want only node 4", got.Nodes)
	}
}

func TestServerPositionsWithoutTheSetting(t *testing.T) {
	got := ServerPositions(context.Background(), mapSettings{}, nil, func(context.Context) string { return "203.0.113.5" })
	if got.Server != nil || len(got.Nodes) != 0 {
		t.Fatalf("positions = %+v, want none without a setting or a location database", got)
	}
	if got := ServerPositions(context.Background(), mapSettings{SettingMapLocations: "{not json"}, nil, nil); got.Server != nil {
		t.Fatalf("a broken setting gave %+v", got.Server)
	}
	if got := ServerPositions(context.Background(), nil, nil, nil); got.Server != nil {
		t.Fatal("no settings gave a server")
	}
}
