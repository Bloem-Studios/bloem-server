package livetv

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/idgen"
	"github.com/Silo-Server/silo-server/internal/secret"
)

// SetXtreamCipher wires the existing server-owned at-rest cipher at startup.
// Without it, provider creation and credential reads fail closed. Router wiring
// can repeat this assignment after the task manager has started, so publication
// must be race-safe. This is not a runtime encryption-key rotation protocol.
func (s *Service) SetXtreamCipher(cipher *secret.Cipher) { s.xtreamCipher.Store(cipher) }

func (s *Service) xtreamStore() (*PgStore, error) {
	store, ok := s.store.(*PgStore)
	if !ok || store == nil || s.xtreamCipher.Load() == nil {
		return nil, ErrNotConfigured
	}
	return store, nil
}

func (s *Service) newXtreamClient(base string, credentials xtreamCredentials) (*xtreamClient, error) {
	client, err := newXtreamClient(base, credentials)
	if err == nil && s.xtreamTransport != nil {
		client.metadata.Transport = s.xtreamTransport
		client.stream.Transport = s.xtreamTransport
		client.guide.Transport = s.xtreamTransport
	}
	return client, err
}

func (s *Service) xtreamClientForTuner(ctx context.Context, tuner *Tuner) (*xtreamClient, error) {
	store, err := s.xtreamStore()
	if err != nil {
		return nil, err
	}
	credentials, err := store.loadXtreamCredentials(ctx, tuner, s.xtreamCipher.Load())
	if err != nil {
		return nil, err
	}
	return s.newXtreamClient(tuner.BaseURL, credentials)
}

func (s *Service) addXtreamTuner(ctx context.Context, in AddTunerInput) (*Tuner, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	store, err := s.xtreamStore()
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if len(name) > 128 || strings.IndexFunc(name, func(r rune) bool { return r < 32 }) >= 0 {
		return nil, fmt.Errorf("%w: provider name is invalid", ErrInvalidArgument)
	}
	if name == "" {
		name = "Xtream"
	}
	capacity := in.MaxConnections
	if capacity == 0 {
		capacity = 1
	}
	if capacity < 1 || capacity > 64 {
		return nil, fmt.Errorf("%w: max_connections must be between 1 and 64", ErrInvalidArgument)
	}
	credentials := xtreamCredentials{Username: in.Username, Password: in.Password}
	client, err := s.newXtreamClient(in.URL, credentials)
	if err != nil {
		return nil, err
	}
	providerCapacity, err := client.authenticate(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	if providerCapacity > 0 && capacity > providerCapacity {
		capacity = providerCapacity
	}
	lineup, err := client.channels(ctx)
	if err != nil {
		return nil, err
	}
	if len(lineup) == 0 {
		return nil, fmt.Errorf("%w: provider has no live channels", ErrInvalidArgument)
	}
	id, err := idgen.NextID()
	if err != nil {
		return nil, err
	}
	tuner := &Tuner{ID: id, Type: TunerTypeXtream, DeviceID: id, BaseURL: client.base.String(), Model: name, TunerCount: capacity}
	channels, err := xtreamChannels(tuner.ID, lineup)
	if err != nil {
		return nil, err
	}
	if err = store.createXtreamTuner(ctx, tuner, credentials, s.xtreamCipher.Load(), channels); err != nil {
		if errors.Is(err, errXtreamDuplicate) {
			return nil, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
		}
		return nil, err
	}
	return store.GetTuner(ctx, id)
}

func xtreamChannels(tunerID string, lineup []xtreamLiveChannel) ([]Channel, error) {
	channels := make([]Channel, 0, len(lineup))
	for _, ch := range lineup {
		id, err := idgen.NextID()
		if err != nil {
			return nil, err
		}
		// The provider's stream ID, not its reorderable display position, is
		// the durable identity. Existing number overrides remain available.
		channels = append(channels, Channel{ID: id, TunerID: tunerID, Number: string(ch.ID), Name: ch.Name, Enabled: true, GuideStationID: ch.EPGChannelID, StreamURL: "xtream://" + tunerID + "/" + string(ch.ID)})
	}
	return channels, nil
}

// Provider stream IDs can exceed int32 and are identities, not OTA channel
// numbers. Preserve provider ordering without multiplying those IDs into the
// legacy integer sort column; HDHomeRun retains its established numeric sort.
func bloemChannelSortKey(ch Channel, index int) int {
	if strings.HasPrefix(ch.StreamURL, "xtream:") {
		return index
	}
	return sortKey(ch.Number, index)
}

func (s *Service) scanXtreamTuner(ctx context.Context, tuner *Tuner) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	client, err := s.xtreamClientForTuner(ctx, tuner)
	if err != nil {
		return err
	}
	capacity, err := client.authenticate(ctx)
	if err != nil {
		return err
	}
	lineup, err := client.channels(ctx)
	if err != nil {
		return err
	}
	if len(lineup) == 0 {
		return errors.New("Xtream returned an empty live lineup; previous channels were retained")
	}
	if capacity > 0 && capacity < tuner.TunerCount {
		store, err := s.xtreamStore()
		if err != nil {
			return err
		}
		if _, err := store.db.Exec(ctx, `UPDATE livetv_tuners SET tuner_count=LEAST(tuner_count,$2),updated_at=now() WHERE id=$1 AND type='xtream'`, tuner.ID, capacity); err != nil {
			return err
		}
	}
	channels, err := xtreamChannels(tuner.ID, lineup)
	if err != nil {
		return err
	}
	return s.store.ReplaceChannelsForTuner(ctx, tuner.ID, channels)
}

func xtreamStreamID(ch *Channel) (string, error) {
	u, err := url.Parse(ch.StreamURL)
	if err != nil || u.Scheme != "xtream" || u.Host != ch.TunerID || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return "", fmt.Errorf("%w: invalid Xtream channel source", ErrInvalidArgument)
	}
	id := strings.TrimPrefix(u.Path, "/")
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 || strconv.FormatInt(n, 10) != id {
		return "", fmt.Errorf("%w: invalid Xtream channel identity", ErrInvalidArgument)
	}
	return id, nil
}
