package registry

import (
	"io/fs"
	"reflect"
	"strings"
	"testing"

	clientv1 "github.com/Silo-Server/silo-server/contracts/client/v1"
)

const valid = `{
  "schema": 1,
  "packages": [
    {"path": "internal/playback", "dialect": "upstream-compat",
     "roots": [{"type": "StartRequestV3", "direction": "request"}]},
    {"path": "internal/api/handlers", "dialect": "upstream-compat", "gate": "pkg.gate",
     "roots": [
       {"type": "capabilityResponse", "direction": "response", "bloem_fields": ["promotions"]},
       {"type": "remoteCapabilityRequest", "direction": "both", "dialect": "bloem", "gate": "notifications.remote_control"}
     ]}
  ],
  "serializers": {"internal/api/handlers.itemListResponse.frame_rate": "org.example.FrameRateWire"}
}`

func TestParseValid(t *testing.T) {
	reg, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if reg.Schema != SchemaVersion || len(reg.Packages) != 2 {
		t.Fatalf("schema=%d packages=%d", reg.Schema, len(reg.Packages))
	}
	handlers := reg.Packages[1]
	if handlers.Roots[0].Direction != DirectionResponse || !reflect.DeepEqual(handlers.Roots[0].BloemFields, []string{"promotions"}) {
		t.Errorf("root 0 = %+v", handlers.Roots[0])
	}
	if got := handlers.EffectiveGate(handlers.Roots[0]); got != "pkg.gate" {
		t.Errorf("inherited gate = %q", got)
	}
	if got := handlers.EffectiveDialect(handlers.Roots[0]); got != DialectUpstreamCompat {
		t.Errorf("inherited dialect = %q", got)
	}
	remote := handlers.Roots[1]
	if remote.Direction != DirectionBoth || !remote.Direction.Request() || !remote.Direction.Response() {
		t.Errorf("direction = %s", remote.Direction)
	}
	if handlers.EffectiveGate(remote) != "notifications.remote_control" || handlers.EffectiveDialect(remote) != DialectBloem {
		t.Errorf("overrides not applied: %+v", remote)
	}
	if got := reg.Serializers[SerializerKey("internal/api/handlers", "itemListResponse", "frame_rate")]; got != "org.example.FrameRateWire" {
		t.Errorf("serializer = %q", got)
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"schema version", strings.Replace(valid, `"schema": 1`, `"schema": 2`, 1), "registry.schema.json"},
		{"unknown key", strings.Replace(valid, `"schema": 1`, `"schema": 1, "extra": true`, 1), "registry.schema.json"},
		{"bad direction", strings.Replace(valid, `"direction": "request"`, `"direction": "sideways"`, 1), "registry.schema.json"},
		{"bad dialect", strings.Replace(valid, `"dialect": "bloem"`, `"dialect": "silo"`, 1), "registry.schema.json"},
		{"missing roots", `{"schema":1,"packages":[{"path":"internal/x","dialect":"bloem","roots":[]}]}`, "registry.schema.json"},
		{"bad path", `{"schema":1,"packages":[{"path":"/abs/path","dialect":"bloem","roots":[{"type":"A","direction":"both"}]}]}`, "registry.schema.json"},
		{"bad serializer key", strings.Replace(valid, `internal/api/handlers.itemListResponse.frame_rate`, `nodots`, 1), "registry.schema.json"},
		{"duplicate package", `{"schema":1,"packages":[
			{"path":"internal/x","dialect":"bloem","roots":[{"type":"A","direction":"both"}]},
			{"path":"internal/x","dialect":"bloem","roots":[{"type":"B","direction":"both"}]}]}`, `package "internal/x" registered twice`},
		{"duplicate root", `{"schema":1,"packages":[{"path":"internal/x","dialect":"bloem","roots":[
			{"type":"A","direction":"both"},{"type":"A","direction":"request"}]}]}`, `root "A" registered twice`},
		{"bloem_fields on bloem root", `{"schema":1,"packages":[{"path":"internal/x","dialect":"bloem","roots":[
			{"type":"A","direction":"both","bloem_fields":["f"]}]}]}`, "bloem_fields only applies to upstream-compat roots"},
		{"serializer for unregistered package", strings.Replace(valid, `internal/api/handlers.itemListResponse`, `internal/other.itemListResponse`, 1), "does not name a registered package"},
		{"not json", `{`, "parsing registry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.body))
			if err == nil {
				t.Fatal("Parse accepted the registry")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q lacks %q", err, tc.want)
			}
		})
	}
}

func TestDirectionRoundTrip(t *testing.T) {
	for _, s := range []string{"request", "response", "both"} {
		d, err := ParseDirection(s)
		if err != nil {
			t.Fatal(err)
		}
		if d.String() != s {
			t.Errorf("%s round-trips to %s", s, d)
		}
		b, err := d.MarshalJSON()
		if err != nil || string(b) != `"`+s+`"` {
			t.Errorf("MarshalJSON(%s) = %s, %v", s, b, err)
		}
	}
	if _, err := ParseDirection("none"); err == nil {
		t.Error("ParseDirection accepted none")
	}
	if Direction(0).String() != "none" {
		t.Error("zero direction must render as none")
	}
}

// TestEmbeddedRegistryIsValid pins the committed registry to its own schema.
func TestEmbeddedRegistryIsValid(t *testing.T) {
	raw, err := fs.ReadFile(clientv1.FS, "registry.json")
	if err != nil {
		t.Fatal(err)
	}
	reg, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Packages) == 0 || reg.Packages[0].Path != "internal/playback" {
		t.Errorf("starter registry packages = %+v", reg.Packages)
	}
}
