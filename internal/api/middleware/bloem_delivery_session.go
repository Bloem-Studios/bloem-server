package middleware

// DeliverySessionID returns the playback session a media delivery path names
// (an HLS playlist or segment, a progressive stream, or a Live TV playlist or
// segment), with the same matching the stream-token check applies, and
// whether the path is a delivery path at all. Stream insights uses it to tie
// a request to its session when the request carries no stream token, as
// header-authenticated media does.
func DeliverySessionID(urlPath string) (string, bool) {
	return streamTokenDeliverySession(urlPath)
}
