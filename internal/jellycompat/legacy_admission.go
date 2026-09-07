package jellycompat

import "net/http"

// The legacy launch/reconstruction is complete before a handler publishes its
// response. Release its database gate at that boundary, not at the end of a
// potentially hours-long media transfer. Error returns still release via defer.
// ResponseController can unwrap this writer for deadlines and connection control.
type legacyAdmissionResponseWriter struct {
	http.ResponseWriter
	release func()
}

func (w *legacyAdmissionResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *legacyAdmissionResponseWriter) WriteHeader(status int) {
	w.release()
	w.ResponseWriter.WriteHeader(status)
}
func (w *legacyAdmissionResponseWriter) Write(data []byte) (int, error) {
	w.release()
	return w.ResponseWriter.Write(data)
}
func (w *legacyAdmissionResponseWriter) Flush() {
	w.release()
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
