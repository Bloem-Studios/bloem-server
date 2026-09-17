package handlers

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/Silo-Server/silo-server/internal/livetv"
)

const liveTVPeerHop = "X-Bloem-LiveTV-Peer-Hop"

// Internal peers are operator-registered API hosts, not outbound media URLs.
// Never use environment proxies, follow redirects, or log credential-bearing
// paths. Reuse connections, but bound dialing, header waits and each segment.
var liveTVPeerTransport = &http.Transport{
	DialContext:           (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   3 * time.Second,
	ResponseHeaderTimeout: 10 * time.Second,
	IdleConnTimeout:       30 * time.Second,
	MaxIdleConnsPerHost:   8,
}

func serveLiveTVPeer(w http.ResponseWriter, r *http.Request, peer string) {
	if r.Header.Get(liveTVPeerHop) != "" {
		liveTVPeerUnavailable(w)
		return
	}
	target, err := url.Parse(peer)
	if err != nil || target.Host == "" || target.User != nil || (target.Scheme != "http" && target.Scheme != "https") || target.RawQuery != "" || target.Fragment != "" || (target.Path != "" && target.Path != "/") {
		liveTVPeerUnavailable(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	proxy := &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.Out.URL.Scheme, p.Out.URL.Host = target.Scheme, target.Host
			p.Out.Host = target.Host
			// Peer reruns normal authentication, permission and session-ownership
			// checks. No new authority is introduced by this routing header.
			p.Out.Header.Set(liveTVPeerHop, "1")
		},
		Transport: liveTVPeerTransport,
		ModifyResponse: func(resp *http.Response) error {
			if resp.StatusCode >= 300 && resp.StatusCode < 400 {
				return errors.New("peer redirect refused")
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) { liveTVPeerUnavailable(w) },
	}
	proxy.ServeHTTP(w, r.WithContext(ctx))
}

func liveTVPeerUnavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "2")
	writeLiveTVError(w, livetv.ErrPeerUnavailable)
}
