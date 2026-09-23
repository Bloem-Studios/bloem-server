// Package lanadvert publishes this server on the LAN as a _bloem._tcp mDNS
// service, so clients on the same L2 broadcast domain can discover it without
// manual origin entry. Every shipped client already browses for the type and
// parses the TXT record; until now no server ever published one.
//
// The record is an unauthenticated hint, not a credential: it carries only
// what a stranger on the LAN could learn from the identity endpoint anyway,
// and it can only push a client toward "not worth attempting". The identity
// endpoint remains the authority on whether a connection is made.
//
// The advertisement is opt-in and degradable. mDNS needs the host's L2
// broadcast domain, which network_mode: host deployments have and
// bridge-networked compose and most LXC setups do not. Where multicast is
// unavailable — or the identity cannot be resolved — the advertiser fails
// quietly once and stays out of the way: no retries, no periodic logging.
package lanadvert

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/brutella/dnssd"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// ServiceType is the DNS-SD service type every shipped client browses.
const ServiceType = "_bloem._tcp"

// txtvers is the TXT contract version. A client that does not recognise the
// value discards the whole record rather than trusting it partially; absent
// is read as 1.
const txtvers = "1"

// Scheme values allowed in the record. The public listener is plain HTTP
// today; TLS terminates at an operator's reverse proxy, whose origin clients
// reach by manual entry — the direct LAN origin this record advertises is
// always the listener's own scheme. If the process ever serves TLS itself,
// the caller must pass the scheme it actually serves.
const (
	SchemeHTTP  = "http"
	SchemeHTTPS = "https"
)

// State is what the advertiser is doing. It exists so wiring and tests can
// distinguish "advertising" from a deliberate quiet skip without guessing
// from logs.
type State int

const (
	// StateInactive means the advertiser is not announcing and never will
	// this process: a precondition failed (identity unresolvable, no
	// multicast-capable interface, registration error) or it was never
	// started.
	StateInactive State = iota

	// StateActive means the service is announced on the network.
	StateActive

	// StateStopped means the service was deregistered cleanly.
	StateStopped
)

// Identity is everything the TXT record publishes, resolved before
// advertising. ServerID must come from the deployment identity service — the
// same value GET /api/bloem/v1/server/identity returns as server_id — and
// ServerName from the same branding name the identity document returns.
type Identity struct {
	ServerID   string
	ServerName string
	Scheme     string
	Port       int
}

// txtRecord builds the exact TXT contract the clients parse. The api and
// bloem lists are read from the identity handler's own variables through the
// exported accessors rather than restated here: the record and the identity
// document must move together, and a literal here would guarantee they
// eventually drift.
func txtRecord(id Identity) map[string]string {
	apiMajors := handlers.ServerAPIMajorVersions()
	api := make([]string, 0, len(apiMajors))
	for _, major := range apiMajors {
		api = append(api, strconv.Itoa(major))
	}
	return map[string]string{
		"txtvers": txtvers,
		"id":      id.ServerID,
		"name":    id.ServerName,
		"scheme":  id.Scheme,
		"api":     strings.Join(api, ","),
		"bloem":   strings.Join(handlers.NativeAPISurfaces(), ","),
	}
}

// IdentitySource answers the deployment's stable server id.
// *serveridentity.Service (and Bloem's serverid.Resolver adapter) satisfy it.
type IdentitySource interface {
	ServerID(ctx context.Context) (string, error)
}

// Config configures Start. Identity and ServerName are required; everything
// else is optional and has a production default.
type Config struct {
	// Listen is the address the public API is served from, e.g. ":8080".
	// Only its port is advertised.
	Listen string

	// Scheme is how the server is actually reachable on the LAN. Empty
	// means http. Anything other than http/https skips advertising.
	Scheme string

	// Identity resolves the server instance id; *serveridentity.Service
	// satisfies it. A source that cannot answer means no advertisement at
	// all — never a made-up identifier, which would silently re-key every
	// client's stored state.
	Identity IdentitySource

	// ServerName returns the operator-facing name, the same one the
	// identity endpoint returns.
	ServerName func(ctx context.Context) string

	// Interfaces lists the interfaces to advertise on; empty means none
	// are usable and the advertiser skips quietly. nil uses the library's
	// own enumeration, so the gate always agrees with what would be bound.
	Interfaces func() []*net.Interface

	// NewResponder builds the mDNS responder. nil uses dnssd.NewResponder.
	// Injectable so tests can exercise registration and deregistration
	// without touching a real network.
	NewResponder func() (dnssd.Responder, error)

	// Logger receives the at-most-one degradation line. nil uses the
	// default slog logger.
	Logger *slog.Logger
}

// Advertiser announces the service for the life of the process. Its zero
// value is an inert StateInactive advertiser whose Stop is a no-op, so a
// skipped start needs no special case at the call site.
type Advertiser struct {
	mu        sync.Mutex
	state     State
	handle    dnssd.ServiceHandle
	responder dnssd.Responder
	cancel    context.CancelFunc
	done      chan struct{}
}

// Start resolves the identity and registers the service, in that order. It
// never blocks and never fails loudly: any precondition that cannot be met
// produces exactly one log line and an advertiser whose Stop is a no-op.
// There is no retry loop by design — a host without multicast does not gain
// one later, and a hostile network is not entitled to a log line per interval.
func Start(ctx context.Context, cfg Config) *Advertiser {
	log := cfg.logger()
	a := &Advertiser{}

	if cfg.Identity == nil {
		log.Debug("LAN advertisement skipped: no server identity resolver configured")
		return a
	}
	if cfg.ServerName == nil {
		log.Debug("LAN advertisement skipped: no server name source configured")
		return a
	}

	port, ok := portFromListen(cfg.Listen)
	if !ok {
		log.Debug("LAN advertisement skipped: listen address has no TCP port",
			"listen", cfg.Listen)
		return a
	}

	scheme := cfg.Scheme
	if scheme == "" {
		scheme = SchemeHTTP
	}
	if scheme != SchemeHTTP && scheme != SchemeHTTPS {
		log.Warn("LAN advertisement skipped: scheme must be http or https",
			"scheme", scheme)
		return a
	}

	// The identity endpoint answers 503 rather than inventing an
	// identifier; the advertiser holds itself to the same rule. A
	// database that has not finished bootstrapping simply means quiet.
	serverID, err := cfg.Identity.ServerID(ctx)
	if err != nil {
		log.Debug("LAN advertisement skipped: server identity unavailable", "error", err)
		return a
	}

	ifaces := cfg.interfaces()
	if len(ifaces) == 0 {
		// The expected shape on bridge-networked compose and most LXC:
		// no interface can join a multicast group, so there is nothing
		// to announce to.
		log.Debug("LAN advertisement skipped: no multicast-capable network interface")
		return a
	}

	name := cfg.ServerName(ctx)
	service, err := dnssd.NewService(dnssd.Config{
		// The mDNS instance label is cosmetic (clients clamp and
		// sanitise it) and the library renames it on a name conflict.
		// The TXT name key below always carries the true server name,
		// and the TXT id key is what clients key state on.
		Name:   name,
		Type:   ServiceType,
		Domain: "local",
		Port:   port,
		Text:   txtRecord(Identity{ServerID: serverID, ServerName: name, Scheme: scheme, Port: port}),
	})
	if err != nil {
		log.Warn("LAN advertisement unavailable: could not build service record", "error", err)
		return a
	}

	respond, err := cfg.newResponder()
	if err != nil {
		log.Warn("LAN advertisement unavailable: could not open mDNS responder", "error", err)
		return a
	}

	advCtx, cancel := context.WithCancel(ctx)
	handle, err := respond.Add(service)
	if err != nil {
		cancel()
		log.Warn("LAN advertisement unavailable: could not register service", "error", err)
		return a
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Respond probes (RFC 6762 §8), announces, and answers
		// queries until advCtx is canceled. A non-cancel error means
		// probing never converged (e.g. a conflict that never
		// resolved); it happens once, so it logs once.
		if err := respond.Respond(advCtx); err != nil && !errors.Is(err, context.Canceled) && advCtx.Err() == nil {
			log.Warn("LAN advertisement stopped: service probing failed", "error", err)
		}
	}()

	a.mu.Lock()
	a.state = StateActive
	a.handle = handle
	a.responder = respond
	a.cancel = cancel
	a.done = done
	a.mu.Unlock()

	log.Info("LAN advertisement registered",
		"type", ServiceType, "port", port, "server_id", serverID)
	return a
}

// Stop deregisters the service — a goodbye packet per the responder's
// announcement set — and stops answering queries. It is safe to call on an
// advertiser that never started and safe to call more than once.
func (a *Advertiser) Stop() {
	a.mu.Lock()
	if a.state != StateActive {
		a.mu.Unlock()
		return
	}
	a.state = StateStopped
	responder, handle, cancel, done := a.responder, a.handle, a.cancel, a.done
	a.mu.Unlock()

	// Remove before cancel: the goodbye packets go out synchronously,
	// while the responder is still running to send them.
	if responder != nil && handle != nil {
		responder.Remove(handle)
	}
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// State reports what the advertiser is doing.
func (a *Advertiser) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (c Config) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

func (c Config) interfaces() []*net.Interface {
	if c.Interfaces != nil {
		return c.Interfaces()
	}
	return dnssd.MulticastInterfaces()
}

func (c Config) newResponder() (dnssd.Responder, error) {
	if c.NewResponder != nil {
		return c.NewResponder()
	}
	return dnssd.NewResponder()
}

// portFromListen extracts the TCP port from a listen address, mirroring how
// the public listener binds it. A listen address without a numeric port
// cannot be advertised.
func portFromListen(addr string) (int, bool) {
	_, portStr, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return 0, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return 0, false
	}
	return port, true
}
