package apiv2

import (
	"net/http"
	"reflect"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
)

const (
	playbackAccountToken  = "token"
	playbackContentLength = "Content-Length"
	playbackLastModified  = "Last-Modified"
	playbackIntegerFormat = "int64"

	playbackMediaBinary        = "application/octet-stream"
	playbackSegmentOperation   = "getPlaybackSegment"
	playbackParamPath          = "path"
	playbackParamQuery         = "query"
	playbackSegmentName        = "name"
	playbackBinaryFormat       = "binary"
	playbackCacheControlHeader = "Cache-Control"
	playbackContentEncoding    = "Content-Encoding"
)

// PlaybackMediaHandlers are the existing grant-aware transports, wrapped by
// the application to admit only the configured initial playback flow.
type PlaybackMediaHandlers struct {
	Original http.Handler
	Manifest http.Handler
	Segment  http.Handler
}

func registerPlaybackDelivery(reg *Registry) {
	var handlers PlaybackMediaHandlers
	if reg.deps.PlaybackMedia != nil {
		handlers = *reg.deps.PlaybackMedia
	}
	for _, route := range []struct {
		method, path, id, protocol string
		handler                    http.Handler
		media                      []string
		ranges                     bool
	}{
		{http.MethodGet, "/stream/{session_id}", "getPlaybackMedia", "media-bytes", handlers.Original, []string{"video/mp4", "video/x-matroska", "video/webm", "video/x-msvideo", "video/quicktime", "video/mp2t", "video/x-flv", "video/x-ms-wmv", "audio/mp4", "audio/mpeg", "audio/flac", "audio/ogg", "audio/wav", "audio/aac", playbackMediaBinary, "multipart/byteranges"}, true},
		{http.MethodHead, "/stream/{session_id}", "headPlaybackMedia", "media-bytes", handlers.Original, nil, true},
		{http.MethodGet, "/playback/transcode/{session_id}/master.m3u8", "getPlaybackManifest", "hls", handlers.Manifest, []string{"application/vnd.apple.mpegurl"}, false},
		{http.MethodGet, "/playback/transcode/{session_id}/segment/{name}", playbackSegmentOperation, "hls", handlers.Segment, []string{"video/mp4", "video/mp2t", playbackMediaBinary, "multipart/byteranges"}, true},
	} {
		params := []*huma.Param{
			{Name: "session_id", In: playbackParamPath, Required: true, Schema: &huma.Schema{Type: huma.TypeString, MinLength: new(1)}},
			{Name: playbackAccountToken, In: playbackParamQuery, Description: "Media-element fallback for the account bearer token when an Authorization header cannot be set. Does not replace the signed executor reference or viewer checks.", Schema: &huma.Schema{Type: huma.TypeString}},
			{Name: "st", In: playbackParamQuery, Required: true, Description: "Opaque signed executor reference returned by playback start; account authentication and viewer authorization are also required.", Schema: &huma.Schema{Type: huma.TypeString}},
		}
		if route.id == playbackSegmentOperation {
			params = append(params, &huma.Param{Name: playbackSegmentName, In: playbackParamPath, Required: true, Schema: &huma.Schema{Type: huma.TypeString, MinLength: new(1)}})
		}
		content := map[string]*huma.MediaType{}
		for _, media := range route.media {
			content[media] = &huma.MediaType{Schema: &huma.Schema{Type: huma.TypeString, Format: playbackBinaryFormat}}
		}
		headers := map[string]*huma.Param{playbackContentLength: {Schema: &huma.Schema{Type: huma.TypeInteger, Format: playbackIntegerFormat}}, playbackCacheControlHeader: {Schema: &huma.Schema{Type: huma.TypeString}}}
		responses := map[string]*huma.Response{"200": {Description: "Playback bytes or HEAD metadata", Content: content, Headers: headers}}
		if route.ranges {
			headers["Accept-Ranges"] = &huma.Param{Schema: &huma.Schema{Type: huma.TypeString}}
			headers[etagField] = &huma.Param{Schema: &huma.Schema{Type: huma.TypeString}}
			headers[playbackLastModified] = &huma.Param{Schema: &huma.Schema{Type: huma.TypeString}}
			responses["206"] = &huma.Response{Description: "Requested byte range", Content: content, Headers: map[string]*huma.Param{"Content-Range": {Schema: &huma.Schema{Type: huma.TypeString}}}}
			responses["304"] = &huma.Response{Description: "The authorized representation has not changed"}
			params = append(params, &huma.Param{Name: ifMatchField, In: paramInHeader, Schema: &huma.Schema{Type: huma.TypeString}}, &huma.Param{Name: "If-Unmodified-Since", In: paramInHeader, Schema: &huma.Schema{Type: huma.TypeString}}, &huma.Param{Name: "Range", In: paramInHeader, Schema: &huma.Schema{Type: huma.TypeString}}, &huma.Param{Name: "If-Range", In: paramInHeader, Schema: &huma.Schema{Type: huma.TypeString}}, &huma.Param{Name: ifNoneMatchField, In: paramInHeader, Schema: &huma.Schema{Type: huma.TypeString}}, &huma.Param{Name: "If-Modified-Since", In: paramInHeader, Schema: &huma.Schema{Type: huma.TypeString}})
		}
		statuses := []int{400, 404, 409, 422, 500, 503}
		if route.ranges {
			statuses = append(statuses, 412, 416)
		}
		for _, status := range statuses {
			responses[strconv.Itoa(status)] = &huma.Response{Description: http.StatusText(status), Content: map[string]*huma.MediaType{problemContentType: {Schema: reg.api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[Problem](), true, "")}}}
		}
		if route.ranges {
			responses["416"].Headers = map[string]*huma.Param{"Content-Range": {Schema: &huma.Schema{Type: huma.TypeString}}}
		}
		raw := RawOperation{Operation: Operation{Operation: huma.Operation{Method: route.method, Path: Prefix + route.path, OperationID: route.id, Tags: []string{"playback"}, Parameters: params, Responses: responses}, Class: ClassProfileScoped, ProfileOptional: true, ServiceBacked: true}, Protocol: route.protocol, Reason: "Grant-authorized media retains native byte, range, HEAD and HLS semantics without JSON buffering."}
		RegisterRaw(reg, raw, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !playbackUUID(chi.URLParam(r, "session_id")) {
				writeProblem(w, r, validationProblem("path.session_id", "invalid", "Expected a canonical UUID."))
				return
			}
			if route.handler == nil {
				writeProblem(w, r, NewProblem(TypeDependencyUnavailable, "Playback delivery is not configured."))
				return
			}
			route.handler.ServeHTTP(&playbackDeliveryWriter{ResponseWriter: w, request: r}, r)
		}))
	}
}

// Playback success bytes pass through immediately. A pre-body transport error
// becomes a safe v2 problem; its legacy JSON/text body is never exposed. Once
// bytes have begun, a transport failure must end the stream, not append JSON.
type playbackDeliveryWriter struct {
	http.ResponseWriter
	request *http.Request
	status  int
	failed  bool
}

func (w *playbackDeliveryWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *playbackDeliveryWriter) WriteHeader(status int) {
	if w.status != 0 {
		if status >= 400 && !w.failed {
			panic(http.ErrAbortHandler)
		}
		return
	}
	if status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	if status < 400 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.failed = true
	for _, header := range []string{playbackContentLength, playbackContentEncoding, "Content-Disposition", jobLocationHeader, etagField, playbackLastModified} {
		w.Header().Del(header)
	}
	kind := TypeForStatus(status)
	writeProblem(w.ResponseWriter, w.request, NewProblem(kind, kind.Title))
}
func (w *playbackDeliveryWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.failed {
		return len(data), nil
	}
	return w.ResponseWriter.Write(data)
}
func (w *playbackDeliveryWriter) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}
