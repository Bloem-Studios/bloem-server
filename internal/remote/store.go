package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// ErrCommandNotFound is returned for an unknown command id.
var ErrCommandNotFound = errors.New("remote command not found")

// Command is one remote_commands row (spec §F).
type Command struct {
	ID              string               `json:"command_id"`
	Scope           Scope                `json:"scope"`
	TargetSessionID string               `json:"session_id,omitempty"`
	TargetDeviceID  string               `json:"device_id,omitempty"`
	TargetUserID    int                  `json:"user_id,omitempty"`
	TargetProfileID string               `json:"profile_id,omitempty"`
	TenantID        string               `json:"tenant_id,omitempty"`
	Name            playback.CommandName `json:"name"`
	Payload         json.RawMessage      `json:"payload"`
	IssuedBy        string               `json:"issued_by"`
	IssuerKind      IssuerKind           `json:"issuer_kind"`
	Reason          string               `json:"reason,omitempty"`
	State           State                `json:"state"`
	Result          json.RawMessage      `json:"result,omitempty"`
	Error           string               `json:"error,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
	SentAt          *time.Time           `json:"sent_at,omitempty"`
	AckedAt         *time.Time           `json:"acked_at,omitempty"`
	FinishedAt      *time.Time           `json:"finished_at,omitempty"`
	ExpiresAt       *time.Time           `json:"expires_at,omitempty"`
}

// DeviceCapability is the persisted remote_control handshake block (§A).
type DeviceCapability struct {
	UserID    int
	ProfileID string
	DeviceID  string
	Version   int
	Commands  []playback.CommandName
	UpdatedAt time.Time
}

// Supports reports whether the device listed name.
func (c *DeviceCapability) Supports(name playback.CommandName) bool {
	if c == nil {
		return false
	}
	for _, listed := range c.Commands {
		if listed == name {
			return true
		}
	}
	return false
}

// AuditQuery pages the audit listing.
type AuditQuery struct {
	SessionID  string
	IssuedBy   string
	IssuerKind IssuerKind
	TenantID   string
	Limit      int
	Offset     int
}

// Store persists commands and device capabilities.
type Store interface {
	Insert(ctx context.Context, command *Command) error
	Get(ctx context.Context, id string) (*Command, error)
	// Transition moves a non-terminal command to state, stamping the matching
	// timestamp. Terminal rows are left alone and reported as ok=false.
	Transition(ctx context.Context, id string, state State, result json.RawMessage, errText string, at time.Time) (bool, error)
	// TransitionOpenSessionCommands applies Transition to every non-terminal
	// command with the given name on a session and returns the ids moved.
	TransitionOpenSessionCommands(ctx context.Context, sessionID string, name playback.CommandName, state State, result json.RawMessage, errText string, at time.Time) ([]string, error)
	ListAudit(ctx context.Context, query AuditQuery) ([]Command, error)
	UpsertDeviceCapability(ctx context.Context, capability DeviceCapability) error
	GetDeviceCapability(ctx context.Context, userID int, profileID, deviceID string) (*DeviceCapability, error)
}

func stampFor(command *Command, state State, at time.Time) {
	switch state {
	case StateSent:
		command.SentAt = &at
	case StateAccepted:
		command.AckedAt = &at
	}
	if state.Terminal() {
		command.FinishedAt = &at
	}
	command.State = state
}

// PostgresStore is the production Store.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore returns a store over pool.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

const commandColumns = `id, scope, target_session_id, target_device_id, target_user_id, target_profile_id, tenant_id,
	name, payload, issued_by, issuer_kind, reason, state, result, error, created_at, sent_at, acked_at, finished_at, expires_at`

func scanCommand(row pgx.Row) (*Command, error) {
	var c Command
	var payload, result []byte
	if err := row.Scan(&c.ID, &c.Scope, &c.TargetSessionID, &c.TargetDeviceID, &c.TargetUserID, &c.TargetProfileID, &c.TenantID,
		&c.Name, &payload, &c.IssuedBy, &c.IssuerKind, &c.Reason, &c.State, &result, &c.Error, &c.CreatedAt, &c.SentAt, &c.AckedAt, &c.FinishedAt, &c.ExpiresAt); err != nil {
		return nil, err
	}
	c.Payload = json.RawMessage(payload)
	if len(result) > 0 {
		c.Result = json.RawMessage(result)
	}
	return &c, nil
}

func (s *PostgresStore) Insert(ctx context.Context, c *Command) error {
	if s == nil || s.pool == nil {
		return errors.New("remote store not configured")
	}
	payload := c.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	var result []byte
	if len(c.Result) > 0 {
		result = c.Result
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO remote_commands (`+commandColumns+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
		c.ID, c.Scope, c.TargetSessionID, c.TargetDeviceID, c.TargetUserID, c.TargetProfileID, c.TenantID,
		string(c.Name), []byte(payload), c.IssuedBy, c.IssuerKind, c.Reason, c.State, result, c.Error, c.CreatedAt, c.SentAt, c.AckedAt, c.FinishedAt, c.ExpiresAt)
	if err != nil {
		return fmt.Errorf("insert remote command: %w", err)
	}
	return nil
}

func (s *PostgresStore) Get(ctx context.Context, id string) (*Command, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("remote store not configured")
	}
	c, err := scanCommand(s.pool.QueryRow(ctx, `SELECT `+commandColumns+` FROM remote_commands WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCommandNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get remote command: %w", err)
	}
	return c, nil
}

func terminalStateList() string {
	return `('rejected','rejected_unsupported','done','failed','expired')`
}

// transitionSQL renders the SET clause with placeholders $p (state), $p+1
// (result), $p+2 (error), $p+3 (timestamp).
func transitionSQL(state State, p int) string {
	set := fmt.Sprintf(`state = $%d, result = COALESCE($%d, result), error = CASE WHEN $%d <> '' THEN $%d ELSE error END`, p, p+1, p+2, p+2)
	switch state {
	case StateSent:
		set += fmt.Sprintf(`, sent_at = $%d`, p+3)
	case StateAccepted:
		set += fmt.Sprintf(`, acked_at = $%d`, p+3)
	}
	if state.Terminal() {
		set += fmt.Sprintf(`, finished_at = $%d`, p+3)
	}
	return set
}

func (s *PostgresStore) Transition(ctx context.Context, id string, state State, result json.RawMessage, errText string, at time.Time) (bool, error) {
	if s == nil || s.pool == nil {
		return false, errors.New("remote store not configured")
	}
	var resultArg []byte
	if len(result) > 0 {
		resultArg = result
	}
	tag, err := s.pool.Exec(ctx, `UPDATE remote_commands SET `+transitionSQL(state, 2)+`
		WHERE id = $1 AND state NOT IN `+terminalStateList(), id, state, resultArg, errText, at)
	if err != nil {
		return false, fmt.Errorf("transition remote command: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *PostgresStore) TransitionOpenSessionCommands(ctx context.Context, sessionID string, name playback.CommandName, state State, result json.RawMessage, errText string, at time.Time) ([]string, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("remote store not configured")
	}
	var resultArg []byte
	if len(result) > 0 {
		resultArg = result
	}
	rows, err := s.pool.Query(ctx, `UPDATE remote_commands SET `+transitionSQL(state, 1)+`
		WHERE target_session_id = $5 AND name = $6 AND state NOT IN `+terminalStateList()+` RETURNING id`,
		state, resultArg, errText, at, sessionID, string(name))
	if err != nil {
		return nil, fmt.Errorf("transition session remote commands: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *PostgresStore) ListAudit(ctx context.Context, query AuditQuery) ([]Command, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("remote store not configured")
	}
	limit, offset := normalizePage(query.Limit, query.Offset)
	var conditions []string
	var args []any
	add := func(clause string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(clause, len(args)))
	}
	if query.SessionID != "" {
		add("target_session_id = $%d", query.SessionID)
	}
	if query.IssuedBy != "" {
		add("issued_by = $%d", query.IssuedBy)
	}
	if query.IssuerKind != "" {
		add("issuer_kind = $%d", string(query.IssuerKind))
	}
	if query.TenantID != "" {
		add("tenant_id = $%d", query.TenantID)
	}
	sql := `SELECT ` + commandColumns + ` FROM remote_commands`
	if len(conditions) > 0 {
		sql += " WHERE " + strings.Join(conditions, " AND ")
	}
	args = append(args, limit, offset)
	sql += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list remote commands: %w", err)
	}
	defer rows.Close()
	commands := []Command{}
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, err
		}
		commands = append(commands, *c)
	}
	return commands, rows.Err()
}

func (s *PostgresStore) UpsertDeviceCapability(ctx context.Context, capability DeviceCapability) error {
	if s == nil || s.pool == nil {
		return errors.New("remote store not configured")
	}
	commands, err := json.Marshal(normalizeCommands(capability.Commands))
	if err != nil {
		return err
	}
	version := capability.Version
	if version <= 0 {
		version = CapabilityVersion
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO remote_device_capabilities (user_id, profile_id, device_id, version, commands, updated_at)
		VALUES ($1,$2,$3,$4,$5,NOW())
		ON CONFLICT (user_id, profile_id, device_id) DO UPDATE SET version = excluded.version, commands = excluded.commands, updated_at = NOW()`,
		capability.UserID, capability.ProfileID, capability.DeviceID, version, commands)
	if err != nil {
		return fmt.Errorf("upsert remote device capability: %w", err)
	}
	return nil
}

func (s *PostgresStore) GetDeviceCapability(ctx context.Context, userID int, profileID, deviceID string) (*DeviceCapability, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("remote store not configured")
	}
	var c DeviceCapability
	var commands []byte
	err := s.pool.QueryRow(ctx, `SELECT user_id, profile_id, device_id, version, commands, updated_at
		FROM remote_device_capabilities WHERE user_id = $1 AND profile_id = $2 AND device_id = $3`,
		userID, profileID, deviceID).Scan(&c.UserID, &c.ProfileID, &c.DeviceID, &c.Version, &commands, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get remote device capability: %w", err)
	}
	if err := json.Unmarshal(commands, &c.Commands); err != nil {
		return nil, fmt.Errorf("decode remote device capability: %w", err)
	}
	return &c, nil
}

func normalizePage(limit, offset int) (int, int) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func normalizeCommands(commands []playback.CommandName) []playback.CommandName {
	seen := map[playback.CommandName]struct{}{}
	out := make([]playback.CommandName, 0, len(commands))
	for _, name := range commands {
		name = playback.CommandName(strings.TrimSpace(string(name)))
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// MemoryStore is an in-process Store for tests and dependency-free wiring.
type MemoryStore struct {
	mu           sync.Mutex
	commands     map[string]*Command
	order        []string
	capabilities map[string]DeviceCapability
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{commands: map[string]*Command{}, capabilities: map[string]DeviceCapability{}}
}

func capabilityKey(userID int, profileID, deviceID string) string {
	return fmt.Sprintf("%d\x00%s\x00%s", userID, profileID, deviceID)
}

func (m *MemoryStore) Insert(_ context.Context, c *Command) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *c
	m.commands[c.ID] = &cp
	m.order = append(m.order, c.ID)
	return nil
}

func (m *MemoryStore) Get(_ context.Context, id string) (*Command, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.commands[id]
	if !ok {
		return nil, ErrCommandNotFound
	}
	cp := *c
	return &cp, nil
}

func (m *MemoryStore) transitionLocked(c *Command, state State, result json.RawMessage, errText string, at time.Time) bool {
	if c.State.Terminal() {
		return false
	}
	if len(result) > 0 {
		c.Result = result
	}
	if errText != "" {
		c.Error = errText
	}
	stampFor(c, state, at)
	return true
}

func (m *MemoryStore) Transition(_ context.Context, id string, state State, result json.RawMessage, errText string, at time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.commands[id]
	if !ok {
		return false, nil
	}
	return m.transitionLocked(c, state, result, errText, at), nil
}

func (m *MemoryStore) TransitionOpenSessionCommands(_ context.Context, sessionID string, name playback.CommandName, state State, result json.RawMessage, errText string, at time.Time) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for _, id := range m.order {
		c := m.commands[id]
		if c.TargetSessionID != sessionID || c.Name != name {
			continue
		}
		if m.transitionLocked(c, state, result, errText, at) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (m *MemoryStore) ListAudit(_ context.Context, query AuditQuery) ([]Command, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	limit, offset := normalizePage(query.Limit, query.Offset)
	matches := []Command{}
	for i := len(m.order) - 1; i >= 0; i-- {
		c := m.commands[m.order[i]]
		if query.SessionID != "" && c.TargetSessionID != query.SessionID {
			continue
		}
		if query.IssuedBy != "" && c.IssuedBy != query.IssuedBy {
			continue
		}
		if query.IssuerKind != "" && c.IssuerKind != query.IssuerKind {
			continue
		}
		if query.TenantID != "" && c.TenantID != query.TenantID {
			continue
		}
		matches = append(matches, *c)
	}
	if offset >= len(matches) {
		return []Command{}, nil
	}
	matches = matches[offset:]
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

func (m *MemoryStore) UpsertDeviceCapability(_ context.Context, capability DeviceCapability) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	capability.Commands = normalizeCommands(capability.Commands)
	if capability.Version <= 0 {
		capability.Version = CapabilityVersion
	}
	capability.UpdatedAt = time.Now()
	m.capabilities[capabilityKey(capability.UserID, capability.ProfileID, capability.DeviceID)] = capability
	return nil
}

func (m *MemoryStore) GetDeviceCapability(_ context.Context, userID int, profileID, deviceID string) (*DeviceCapability, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.capabilities[capabilityKey(userID, profileID, deviceID)]
	if !ok {
		return nil, nil
	}
	cp := c
	cp.Commands = append([]playback.CommandName(nil), c.Commands...)
	return &cp, nil
}
