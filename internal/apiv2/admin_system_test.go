package apiv2

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/nodemetrics"
)

type fakeAdminResourceSampler struct {
	snapshot nodemetrics.Snapshot
	reads    int
}

func (f *fakeAdminResourceSampler) Snapshot() nodemetrics.Snapshot { f.reads++; return f.snapshot }

func TestAdminSystemResourcesAuthorizationAndProjection(t *testing.T) {
	f := &fakeAdminResourceSampler{snapshot: nodemetrics.Snapshot{Available: true, SampledAt: time.Date(2026, 9, 5, 1, 2, 3, 123456789, time.FixedZone("test", 3600)), System: &nodemetrics.SystemStats{CPUPct: 17}, GPU: []nodemetrics.GPUStats{{Device: "gpu-test", Source: "fdinfo", VideoBusyPct: new(0)}}}}
	deps := requestDeps(fixtureRequests())
	deps.AdminResourceSampler = f
	h := NewHandler(deps)
	path := Prefix + "/admin/system/resources"
	denied := do(t, h, http.MethodGet, path, "", nil)
	if denied.Code != http.StatusUnauthorized || f.reads != 0 {
		t.Fatalf("unauthorized sample: %d reads %d", denied.Code, f.reads)
	}
	read := do(t, h, http.MethodGet, path, "", actingRequestAdmin)
	if read.Code != http.StatusOK {
		t.Fatal(read.Code, read.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(read.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["sampled_at"] != "2026-09-05T00:02:03.123Z" {
		t.Fatal(body)
	}
	if !strings.Contains(read.Body.String(), `"disks":[]`) || !strings.Contains(read.Body.String(), `"video_busy_pct":0`) || strings.Contains(read.Body.String(), `"total_busy_pct"`) {
		t.Fatal(read.Body.String())
	}
	if f.reads != 1 || f.snapshot.System.Disks != nil {
		t.Fatal("read mutated snapshot or sampled more than once")
	}
}
func TestAdminSystemResourcesUnsampled(t *testing.T) {
	h := NewHandler(requestDeps(fixtureRequests()))
	read := do(t, h, http.MethodGet, Prefix+"/admin/system/resources", "", actingRequestAdmin)
	if read.Code != 200 || !strings.Contains(read.Body.String(), `"available":false`) || !strings.Contains(read.Body.String(), `"gpu":[]`) || strings.Contains(read.Body.String(), `"sampled_at"`) {
		t.Fatal(read.Code, read.Body.String())
	}
}
func TestAdminSystemBuild(t *testing.T) {
	h := NewHandler(requestDeps(fixtureRequests()))
	read := do(t, h, http.MethodGet, Prefix+"/admin/system/build", "", actingRequestAdmin)
	if read.Code != 200 || !strings.Contains(read.Body.String(), `"build_number":`) {
		t.Fatal(read.Code, read.Body.String())
	}
	for _, raw := range []string{"", "2026-09-05T01:02:03.123456Z", "invalid"} {
		value, err := buildInstant(raw)
		switch raw {
		case "":
			if value != nil || err != nil {
				t.Fatal(value, err)
			}
		case "invalid":
			if err == nil {
				t.Fatal("invalid timestamp accepted")
			}
		default:
			if err != nil || value.String() != "2026-09-05T01:02:03.123Z" {
				t.Fatal(value, err)
			}
		}
	}
}
