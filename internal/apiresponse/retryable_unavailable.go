package apiresponse

import (
	"encoding/json"
	"net/http"
	"strconv"
)

const defaultRetryAfterSeconds = 1

type retryableUnavailableBody struct {
	Error      string `json:"error"`
	Message    string `json:"message"`
	RetryAfter int    `json:"retry_after"`
}

// WriteRetryableUnavailable emits the one stable compatibility/pending 503
// shape. The retry interval is deliberately short and bounded; handlers never
// retry unsafe requests themselves.
func WriteRetryableUnavailable(w http.ResponseWriter, code, message string, retryAfterSeconds int) {
	if retryAfterSeconds < 1 {
		retryAfterSeconds = defaultRetryAfterSeconds
	} else if retryAfterSeconds > 30 {
		retryAfterSeconds = 30
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(retryableUnavailableBody{
		Error: code, Message: message, RetryAfter: retryAfterSeconds,
	})
}
