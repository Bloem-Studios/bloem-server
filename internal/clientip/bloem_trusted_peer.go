package clientip

import "net/http"

// PeerTrusted reports whether req's transport peer is inside the trusted
// proxy CIDRs. A nil resolver trusts no peer. Bloem-owned: stream insights
// accepts an edge's X-Bloem-Edge and CF-Ray headers only from such a peer.
func (r *Resolver) PeerTrusted(req *http.Request) bool {
	return r.peerTrusted(req)
}
