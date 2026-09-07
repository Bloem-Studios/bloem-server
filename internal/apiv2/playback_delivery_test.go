package apiv2

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/playback"
)

const deliveryTestSession = "11111111-1111-4111-8111-111111111111"

func TestPlaybackDeliveryV2BytesErrorsAndAuthorization(t *testing.T) {
	deps, _ := catalogDeps(t)
	calls := 0
	deps.PlaybackMedia = &PlaybackMediaHandlers{Original: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("st") != "opaque" {
			t.Error("signed reference changed")
		}
		w.Header().Set("Content-Type", "video/mp4")
		http.ServeContent(w, r, "fixture.mp4", time.Time{}, strings.NewReader("0123456789"))
	})}
	h := newTestHandler(t, deps)
	path := Prefix + "/stream/" + deliveryTestSession + "?st=opaque"
	full := do(t, h, http.MethodGet, path, "", viewerHeaders())
	if full.Code != 200 || full.Body.String() != "0123456789" {
		t.Fatalf("full bytes: %d %q", full.Code, full.Body.String())
	}
	rangeResponse := do(t, h, http.MethodGet, path, "", with(viewerHeaders(), "Range", "bytes=2-4"))
	if rangeResponse.Code != 206 || rangeResponse.Body.String() != "234" || rangeResponse.Header().Get("Content-Range") != "bytes 2-4/10" {
		t.Fatalf("range: %d %s %v", rangeResponse.Code, rangeResponse.Body.String(), rangeResponse.Header())
	}
	head := do(t, h, http.MethodHead, path, "", viewerHeaders())
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != "10" {
		t.Fatalf("HEAD: %d %s %v", head.Code, head.Body.String(), head.Header())
	}
	invalidRange := do(t, h, http.MethodGet, path, "", with(viewerHeaders(), "Range", "bytes=30-40"))
	requireProblem(t, invalidRange, TypeRangeNotSatisfiable)
	if invalidRange.Header().Get("Content-Range") != "bytes */10" || invalidRange.Header().Get("Content-Length") != "" {
		t.Fatalf("range problem metadata: %v", invalidRange.Header())
	}
	queryAuth := do(t, h, http.MethodGet, path+"&token="+memberToken, "", nil)
	if queryAuth.Code != 200 || queryAuth.Body.String() != "0123456789" {
		t.Fatalf("media query auth: %d %s", queryAuth.Code, queryAuth.Body.String())
	}
	before := calls
	requireProblem(t, do(t, h, http.MethodGet, path, "", with(bearer(memberToken), "X-Profile-Id", "p-other")), TypeNotFound)
	if calls != before {
		t.Fatal("rejected viewer reached media")
	}
	deps.PlaybackMedia = nil
	requireProblem(t, do(t, newTestHandler(t, deps), http.MethodGet, path, "", viewerHeaders()), TypeDependencyUnavailable)
}

func TestPlaybackDeliveryProblemDropsLegacyBodyAndHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, Prefix+"/stream/"+deliveryTestSession, nil)
	writer := &playbackDeliveryWriter{ResponseWriter: w, request: r}
	writer.Header().Set("Content-Length", "10000")
	writer.Header().Set("Location", "PRIVATE_LOCATION")
	writer.Header().Set("Content-Encoding", "gzip")
	http.Error(writer, "PRIVATE_ERROR", http.StatusServiceUnavailable)
	if w.Code != 503 || strings.Contains(w.Body.String(), "PRIVATE") || w.Header().Get("Content-Length") != "" || w.Header().Get("Location") != "" || w.Header().Get("Content-Encoding") != "" {
		t.Fatalf("unsafe error: %d %s %v", w.Code, w.Body.String(), w.Header())
	}
}

func TestPlaybackDeliveryFailureAfterBytesAborts(t *testing.T) {
	w := httptest.NewRecorder()
	writer := &playbackDeliveryWriter{ResponseWriter: w, request: httptest.NewRequest(http.MethodGet, "/", nil)}
	_, _ = io.WriteString(writer, "first")
	defer func() {
		got := recover()
		err, ok := got.(error)
		if !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Fatalf("late failure did not abort: %v", got)
		}
		if w.Body.String() != "first" {
			t.Fatalf("appended error bytes: %q", w.Body.String())
		}
	}()
	http.Error(writer, "PRIVATE_ERROR", 500)
}

func TestPlaybackDecisionV2ProjectsOnlyLocalMediaURLs(t *testing.T) {
	for _, url := range []string{"/api/v1/stream/session?st=opaque%2Btoken", "/api/v1/playback/transcode/session/master.m3u8?st=opaque%2Btoken", "https://silo.example.test/opaque"} {
		in := playback.DecisionResponseV3{PlaybackPlan: &playback.PlanV3{Stream: playback.StreamV3{URL: url}}}
		out := playbackDecision(in)
		want := url
		if strings.HasPrefix(url, "/api/v1/") {
			want = Prefix + strings.TrimPrefix(url, "/api/v1")
		}
		if out.PlaybackPlan.Stream.URL != want || in.PlaybackPlan.Stream.URL != url {
			t.Fatalf("projection changed source or signed query: %q %q", out.PlaybackPlan.Stream.URL, in.PlaybackPlan.Stream.URL)
		}
	}
}

func TestPlaybackSubtitleDeliveryV2(t *testing.T) {
	deps, _ := catalogDeps(t)
	subtitleCalls, fontCalls := 0, 0
	deps.PlaybackMedia = &PlaybackMediaHandlers{
		Subtitle: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			subtitleCalls++
			if r.URL.Query().Get("st") != "opaque" || r.URL.Query().Get("file_id") != "42" {
				t.Error("signed reference or source file changed")
			}
			w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			_, _ = w.Write([]byte("WEBVTT\n\n00:00.000 --> 00:01.000\ncue\n"))
		}),
		SubtitleFonts: fakeSubtitleFonts{calls: &fontCalls},
	}
	h := newTestHandler(t, deps)
	path := Prefix + "/stream/" + deliveryTestSession + "/subtitles/0.vtt?file_id=42&st=opaque"
	get := do(t, h, http.MethodGet, path, "", viewerHeaders())
	if get.Code != 200 || !strings.Contains(get.Body.String(), "WEBVTT") || get.Header().Get("Content-Type") != "text/vtt; charset=utf-8" {
		t.Fatalf("subtitle: %d %q %v", get.Code, get.Body.String(), get.Header())
	}
	head := do(t, h, http.MethodHead, path, "", viewerHeaders())
	if head.Code != 200 || head.Body.Len() != 0 {
		t.Fatalf("HEAD: %d %q", head.Code, head.Body.String())
	}
	fonts := do(t, h, http.MethodGet, Prefix+"/stream/"+deliveryTestSession+"/subtitles/1/fonts?file_id=42&st=opaque", "", viewerHeaders())
	if fonts.Code != 200 || fonts.Body.String() != `[{"name":"synthetic.ttf","data":"AAEC"}]`+"\n" || fonts.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("fonts: %d %q %v", fonts.Code, fonts.Body.String(), fonts.Header())
	}
	// The producer's refusals are Problems: a 400 becomes 422, a 404 stays, a
	// route refusal stays 503, and a missing signed reference is a validation
	// failure before the producer runs.
	requireProblem(t, do(t, h, http.MethodGet, Prefix+"/stream/"+deliveryTestSession+"/subtitles/1/fonts?file_id=42", "", viewerHeaders()), TypeValidationFailed)
	for _, refusal := range []struct {
		err  *handlers.APIError
		want ProblemType
	}{
		{&handlers.APIError{Status: http.StatusBadRequest, Code: "bad_request", Message: "Subtitle font bundles are only available for ASS/SSA tracks"}, TypeValidationFailed},
		{&handlers.APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Embedded subtitle track not found"}, TypeNotFound},
		{&handlers.APIError{Status: http.StatusServiceUnavailable, Code: "routing_policy_unsatisfied", Message: "The media request does not match the route bound by the playback plan"}, TypeDependencyUnavailable},
	} {
		fontErr = refusal.err
		requireProblem(t, do(t, h, http.MethodGet, Prefix+"/stream/"+deliveryTestSession+"/subtitles/1/fonts?st=opaque", "", viewerHeaders()), refusal.want)
	}
	fontErr = nil
	// Two sidecar calls (GET, HEAD) and one accepted font call plus the three
	// refusals the fake produced; the missing-reference case never reached it.
	if subtitleCalls != 2 || fontCalls != 4 {
		t.Fatalf("calls = %d/%d", subtitleCalls, fontCalls)
	}
	// Same gates as media bytes: a non-UUID session is a validation problem, a
	// rejected viewer never reaches the producer, an unconfigured producer is 503.
	requireProblem(t, do(t, h, http.MethodGet, Prefix+"/stream/not-a-uuid/subtitles/0.vtt?st=opaque", "", viewerHeaders()), TypeValidationFailed)
	before := subtitleCalls
	requireProblem(t, do(t, h, http.MethodGet, path, "", with(bearer(memberToken), "X-Profile-Id", "p-other")), TypeNotFound)
	if subtitleCalls != before {
		t.Fatal("rejected viewer reached the subtitle producer")
	}
	deps.PlaybackMedia = &PlaybackMediaHandlers{}
	requireProblem(t, do(t, newTestHandler(t, deps), http.MethodGet, path, "", viewerHeaders()), TypeDependencyUnavailable)
	requireProblem(t, do(t, newTestHandler(t, deps), http.MethodGet, Prefix+"/stream/"+deliveryTestSession+"/subtitles/1/fonts?st=opaque", "", viewerHeaders()), TypeDependencyUnavailable)
}

var fontErr error

type fakeSubtitleFonts struct{ calls *int }

func (f fakeSubtitleFonts) BoundSubtitleFontBundle(_ http.ResponseWriter, r *http.Request) ([]playback.SubtitleFontBundleItem, error) {
	*f.calls++
	if fontErr != nil {
		return nil, fontErr
	}
	if r.URL.Query().Get("st") != "opaque" || r.URL.Query().Get("file_id") != "42" {
		return nil, errors.New("signed reference or source file changed")
	}
	return []playback.SubtitleFontBundleItem{{Name: "synthetic.ttf", Data: "AAEC"}}, nil
}

func TestPlaybackDecisionV2ProjectsSubtitleURLsWithoutMutatingSource(t *testing.T) {
	in := playback.DecisionResponseV3{PlaybackPlan: &playback.PlanV3{Stream: playback.StreamV3{URL: "/api/v1/stream/s?st=x"}, Subtitle: playback.SubtitleDecisionV3{
		Artifact: &playback.SubtitleArtifactV3{URL: "/api/v1/stream/s/subtitles/1.ass?file_id=42&st=x"},
		Inventory: []playback.SubtitleInventoryItemV3{
			{CombinedIndex: 0, URL: "/api/v1/stream/s/subtitles/0.vtt?file_id=42&st=x"},
			{CombinedIndex: 1, URL: "/api/v1/stream/s/subtitles/1.ass?file_id=42&st=x", FontBundleURL: "/api/v1/stream/s/subtitles/1/fonts?file_id=42&st=x"},
			{CombinedIndex: 2, URL: "/stream/legacy/subtitles/2.vtt?file_id=42"},
		}}}}
	out := playbackDecision(in)
	sub := out.PlaybackPlan.Subtitle
	if sub.Artifact.URL != Prefix+"/stream/s/subtitles/1.ass?file_id=42&st=x" || sub.Inventory[0].URL != Prefix+"/stream/s/subtitles/0.vtt?file_id=42&st=x" || sub.Inventory[1].FontBundleURL != Prefix+"/stream/s/subtitles/1/fonts?file_id=42&st=x" || sub.Inventory[2].URL != "/stream/legacy/subtitles/2.vtt?file_id=42" {
		t.Fatalf("projection: %+v %+v", sub.Artifact, sub.Inventory)
	}
	if in.PlaybackPlan.Subtitle.Artifact.URL != "/api/v1/stream/s/subtitles/1.ass?file_id=42&st=x" || in.PlaybackPlan.Subtitle.Inventory[0].URL != "/api/v1/stream/s/subtitles/0.vtt?file_id=42&st=x" {
		t.Fatal("projection mutated the persisted decision")
	}
}
