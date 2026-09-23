package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// strictReconstructAdmission reports the operator's admission posture for a
// reconstruct whose limit provider could not be evaluated. Defaults to upstream
// Silo's fail-open behavior when the setting is unset or unreadable — a
// settings-store outage must not itself become the reason playback is refused.
func (h *PlaybackHandler) strictReconstructAdmission() bool {
	if h.SettingsRepo == nil {
		return false
	}
	v, err := h.SettingsRepo.Get(context.Background(), config.PlaybackStrictReconstructAdmissionSettingKey)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(v), "true")
}

type bufferedPlaybackResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferedPlaybackResponse() *bufferedPlaybackResponse {
	return &bufferedPlaybackResponse{header: make(http.Header), status: http.StatusOK}
}

func (w *bufferedPlaybackResponse) Header() http.Header { return w.header }

func (w *bufferedPlaybackResponse) WriteHeader(status int) { w.status = status }

func (w *bufferedPlaybackResponse) Write(p []byte) (int, error) { return w.body.Write(p) }

func flushSiloApplePlaybackV3Response(w http.ResponseWriter, buffered *bufferedPlaybackResponse) {
	body := buffered.body.Bytes()
	if buffered.status >= 200 && buffered.status < 300 {
		var response map[string]any
		if json.Unmarshal(body, &response) == nil {
			if plan, ok := response["playback_plan"].(map[string]any); ok {
				delivery, _ := plan["delivery"].(string)
				switch delivery {
				case string(playback.DeliveryOriginalHTTPV3):
					plan["engine"] = "media3_direct"
				case string(playback.DeliveryRemuxProgressiveV3):
					plan["engine"] = "media3_progressive_remux"
				case string(playback.DeliveryRemuxHLSV3), string(playback.DeliveryTranscodeHLSV3):
					plan["engine"] = "media3_hls"
				}
				if encoded, err := json.Marshal(response); err == nil {
					body = encoded
				}
			}
		}
	}
	for key, values := range buffered.header {
		w.Header()[key] = append([]string(nil), values...)
	}
	w.WriteHeader(buffered.status)
	_, _ = w.Write(body)
}

// normalizeSiloApplePlaybackV3Body translates the last Silo Apple v3 wire
// shape into the finalized platform-neutral shape. That client already sends
// protocol v3 and a complete engine/capability inventory, but predates the
// evidence markers and the engines-to-deliveries rename. Treating its claims
// as the weakest evidence tier keeps planning conservative.
func normalizeSiloApplePlaybackV3Body(body []byte) ([]byte, bool, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return body, false, err
	}
	var version int
	if err := json.Unmarshal(root["protocol_version"], &version); err != nil || version != playback.ProtocolV3 {
		return body, false, nil
	}
	var context map[string]json.RawMessage
	if err := json.Unmarshal(root["client_playback_context"], &context); err != nil {
		return body, false, nil
	}
	var platform string
	if err := json.Unmarshal(context["platform"], &platform); err != nil ||
		(platform != "ios" && platform != "tvos" && platform != "macos") {
		return body, false, nil
	}
	engines, hasEngines := context["engines"]
	if !hasEngines {
		return body, false, nil
	}
	if _, hasDeliveries := context["deliveries"]; !hasDeliveries {
		var legacyEngines map[string]json.RawMessage
		if err := json.Unmarshal(engines, &legacyEngines); err != nil {
			return body, false, nil
		}
		deliveries := make(map[string]json.RawMessage, 3)
		if direct, ok := legacyEngines["media3_direct"]; ok {
			deliveries[playback.DeliveryClassOriginalHTTPV3] = direct
		}
		if progressive, ok := legacyEngines["media3_progressive_remux"]; ok {
			deliveries[playback.DeliveryClassProgressiveV3] = progressive
		}
		if hls, ok := legacyEngines["media3_hls"]; ok {
			deliveries[playback.DeliveryClassHLSV3] = hls
		}
		context["deliveries"], _ = json.Marshal(deliveries)
	}

	var capabilities map[string]json.RawMessage
	if err := json.Unmarshal(root["client_capabilities"], &capabilities); err != nil {
		return body, false, nil
	}
	declared, _ := json.Marshal(playback.EvidenceDeclaredV3)
	if _, ok := capabilities["video_evidence"]; !ok {
		capabilities["video_evidence"] = declared
	}
	if _, ok := capabilities["audio_evidence"]; !ok {
		capabilities["audio_evidence"] = declared
	}

	var device map[string]json.RawMessage
	if err := json.Unmarshal(context["device"], &device); err == nil {
		if _, ok := device["platform"]; !ok {
			device["platform"], _ = json.Marshal(platform)
		}
		context["device"], _ = json.Marshal(device)
	}
	var output map[string]json.RawMessage
	if err := json.Unmarshal(context["output"], &output); err == nil {
		if generation, ok := output["output_route_generation"]; ok {
			if _, exists := output["output_context_id"]; !exists {
				var value int64
				if json.Unmarshal(generation, &value) == nil {
					output["output_context_id"], _ = json.Marshal(strconv.FormatInt(value, 10))
				}
			}
		}
		context["output"], _ = json.Marshal(output)
	}
	root["client_capabilities"], _ = json.Marshal(capabilities)
	root["client_playback_context"], _ = json.Marshal(context)
	normalized, err := json.Marshal(root)
	return normalized, true, err
}
