package apiv2

import (
	"strings"
	"testing"
)

// Live TV storage that isn't set up is a 503 on every route that reaches it,
// as on Xtream provider creation; a synchronization already running is a 409.
func TestBloemLiveTVDocumentNamesUnavailableStorageAndSyncConflicts(t *testing.T) {
	doc := bloemWebDocument(t)
	description := func(path, method, status string) string {
		t.Helper()
		text, _ := bloemDocObject(t, doc, "paths", BloemPrefix+path, method, "responses", status)["description"].(string)
		return text
	}
	for _, route := range []struct{ path, method string }{
		{"/livetv/tuners/{tunerId}", "delete"},
		{"/livetv/tuners/{tunerId}/scan", "post"},
		{"/livetv/guide-sources", "post"},
		{"/livetv/guide-sources/{sourceId}", "patch"},
		{"/livetv/guide-sources/{sourceId}/sync", "post"},
		{"/livetv/sessions/{sessionId}/stream", "get"},
		{"/livetv/sessions/{sessionId}/stream", "head"},
	} {
		if strings.Contains(description(route.path, route.method, "500"), "storage") || !strings.Contains(description(route.path, route.method, "503"), "dependency_unavailable: encrypted Xtream provider storage is unavailable.") {
			t.Errorf("%s %s does not answer unavailable storage with 503", route.method, route.path)
		}
	}

	sync := "/livetv/guide-sources/{sourceId}/sync"
	if strings.Contains(description(sync, "post", "500"), "already running") || !strings.Contains(description(sync, "post", "409"), "conflict: an Xtream synchronization of this source is already running") {
		t.Error("a running Xtream synchronization is not documented as 409 conflict")
	}

	add := bloemDocObject(t, doc, "paths", BloemPrefix+"/livetv/tuners", "post")
	if text, _ := add["description"].(string); strings.Contains(text, "answers 500") || !strings.Contains(text, "answers 201 with status error") {
		t.Errorf("adding a tuner whose scan fails is not documented as 201: %s", text)
	}
}
