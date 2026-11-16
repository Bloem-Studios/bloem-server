package orglive

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	// probeInterval is how long the listener waits for a notification before
	// it checks that its connection is still alive. A half-open TCP connection
	// would otherwise look like a quiet database.
	probeInterval = 30 * time.Second
	dialTimeout   = 10 * time.Second
	minBackoff    = time.Second
	maxBackoff    = 30 * time.Second
)

// notifyConn is the slice of a Postgres connection the listener uses.
type notifyConn interface {
	// Listen subscribes the connection to the NOTIFY channel.
	Listen(ctx context.Context, channel string) error
	// Wait blocks for the next payload, or until ctx ends.
	Wait(ctx context.Context) (string, error)
	// Ping checks that the connection answers.
	Ping(ctx context.Context) error
	Close(ctx context.Context) error
}

// Listener holds one dedicated LISTEN connection per node and hands what the
// database triggers send to the Bus. It owns the Bus's health: while it has no
// working connection the Bus is down, which refuses new sockets and closes
// open ones, so no client sits on a socket that cannot hear anything.
type Listener struct {
	bus    *Bus
	dial   func(context.Context) (notifyConn, error)
	logger *slog.Logger

	probe      time.Duration
	minBackoff time.Duration
	maxBackoff time.Duration

	once sync.Once
}

// NewListener returns a listener that connects with config. The connection is
// opened outside any pool: a pooled connection would hold a pool slot for as
// long as the server runs, and a transaction-pooling proxy cannot LISTEN at
// all, so config must reach Postgres directly.
func NewListener(bus *Bus, config *pgx.ConnConfig, logger *slog.Logger) *Listener {
	return newListener(bus, func(ctx context.Context) (notifyConn, error) {
		ctx, cancel := context.WithTimeout(ctx, dialTimeout)
		defer cancel()
		conn, err := pgx.ConnectConfig(ctx, config.Copy())
		if err != nil {
			return nil, err
		}
		return pgxNotifyConn{conn}, nil
	}, logger)
}

func newListener(bus *Bus, dial func(context.Context) (notifyConn, error), logger *slog.Logger) *Listener {
	if logger == nil {
		logger = slog.Default()
	}
	return &Listener{bus: bus, dial: dial, logger: logger, probe: probeInterval, minBackoff: minBackoff, maxBackoff: maxBackoff}
}

// Start runs the listener in the background the first time it is called, and
// does nothing after. A node that never serves an organization live socket never
// opens the connection, and a node that does opens it on first use.
func (l *Listener) Start(ctx context.Context) {
	l.once.Do(func() { go l.Run(ctx) })
}

// Run listens until ctx ends. It never returns an error: a lost connection is
// retried with backoff, and the Bus reports the gap. When ctx ends the Bus
// shuts down, closing the open sockets with a restart code.
func (l *Listener) Run(ctx context.Context) {
	defer l.bus.Shutdown()
	backoff := l.minBackoff
	for ctx.Err() == nil {
		started := time.Now()
		err := l.session(ctx)
		if ctx.Err() != nil {
			// Stopping, not failing: the deferred Shutdown closes the sockets
			// with the restart code, not the feed-down one.
			return
		}
		l.bus.SetHealthy(false)
		// A session that held for a while earns a fresh, short backoff.
		if time.Since(started) > l.maxBackoff {
			backoff = l.minBackoff
		}
		l.logger.WarnContext(ctx, "organization live change feed lost; reconnecting", "component", "orglive", "error", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, l.maxBackoff)
	}
}

// session runs one connection until it fails.
func (l *Listener) session(ctx context.Context) error {
	conn, err := l.dial(ctx)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	if err := conn.Listen(ctx, NotifyChannel); err != nil {
		return err
	}
	// Anything signaled while the feed was down was missed. No socket survives
	// that: SetHealthy(false) closed them all and Register refuses new ones
	// until now, and a client that reconnects gets a resync with its subscribe.
	l.bus.SetHealthy(true)
	l.logger.InfoContext(ctx, "organization live change feed listening", "component", "orglive")
	for {
		waitCtx, cancel := context.WithTimeout(ctx, l.probe)
		payload, err := conn.Wait(waitCtx)
		cancel()
		switch {
		case err == nil:
			l.deliver(payload)
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, context.DeadlineExceeded):
			pingCtx, stop := context.WithTimeout(ctx, dialTimeout)
			pingErr := conn.Ping(pingCtx)
			stop()
			if pingErr != nil {
				return pingErr
			}
		default:
			return err
		}
	}
}

func (l *Listener) deliver(payload string) {
	org, change, ok := ParseNotification(payload)
	if !ok {
		// Never guess an organization for a payload we cannot read.
		l.logger.Warn("organization live signal dropped", "component", "orglive", "reason", "unreadable")
		return
	}
	l.bus.Publish(org, change)
}

type pgxNotifyConn struct{ conn *pgx.Conn }

func (c pgxNotifyConn) Listen(ctx context.Context, channel string) error {
	_, err := c.conn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize())
	return err
}

func (c pgxNotifyConn) Wait(ctx context.Context) (string, error) {
	notification, err := c.conn.WaitForNotification(ctx)
	if err != nil {
		return "", err
	}
	return notification.Payload, nil
}

func (c pgxNotifyConn) Ping(ctx context.Context) error { return c.conn.Ping(ctx) }

func (c pgxNotifyConn) Close(ctx context.Context) error { return c.conn.Close(ctx) }
