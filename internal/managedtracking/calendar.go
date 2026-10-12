package managedtracking

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	calendar "github.com/Silo-Server/silo-server/internal/managedtracking/calendarwire"
)

const calendarEventLimit = 10000

// CalendarSource is composed by the API owner. It resolves current profile
// authority using the ordinary host policy and calendar projection.
type calendarSourceBox struct{ source CalendarSource }

// SetCalendarSource publishes an immutable adapter after host composition.
func (s *Service) SetCalendarSource(source CalendarSource) {
	s.calendarConfigured.Store(&calendarSourceBox{source: source})
}
func (s *Service) calendarSource() CalendarSource {
	if s == nil {
		return nil
	}
	if s.Calendar != nil {
		return s.Calendar
	}
	if source := s.calendarConfigured.Load(); source != nil {
		return source.source
	}
	return nil
}

type CalendarSource interface {
	Authority(context.Context, Profile) (string, error)
	Events(context.Context, Profile, time.Time, time.Time) ([]*calendar.CalendarEvent, error)
}

func calendarDigest(scope string, payload []byte) string {
	h := sha256.New()
	h.Write([]byte(scope))
	h.Write([]byte{0})
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}
func calendarPayload(events []*calendar.CalendarEvent) ([]byte, error) {
	return (proto.MarshalOptions{Deterministic: true}).Marshal(&calendar.CalendarPage{Events: events})
}

func calendarWindow(r *calendar.CalendarRequest) (time.Time, time.Time, error) {
	if r == nil || !positiveID(r.AccountId) || r.ProfileId == "" || len(r.ProfileId) > 128 || r.Limit < 1 || r.Limit > 100 {
		return time.Time{}, time.Time{}, ErrSnapshot
	}
	start, e1 := time.Parse("2006-01-02", r.Start)
	end, e2 := time.Parse("2006-01-02", r.End)
	if e1 != nil || e2 != nil || end.Before(start) || end.Sub(start) > 30*24*time.Hour {
		return time.Time{}, time.Time{}, ErrSnapshot
	}
	return start, end, nil
}
func calendarProfile(ctx context.Context, tx pgx.Tx, scope, account, profile string) (Profile, error) {
	var p Profile
	err := tx.QueryRow(ctx, `SELECT account_id::text,profile_id,default_profile_id,account_name,name,email,account_revision,revision,account_active,active,identity_issuer,identity_subject FROM bloem_managed_profiles WHERE tenant_id=$1 AND account_id=$2 AND profile_id=$3 FOR SHARE`, scope, account, profile).Scan(&p.AccountID, &p.ProfileID, &p.DefaultProfileID, &p.AccountName, &p.Name, &p.Email, &p.AccountRevision, &p.Revision, &p.AccountActive, &p.Active, &p.VerifiedIdentityIssuer, &p.VerifiedIdentitySubject)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (!p.Active || !p.AccountActive) {
		return Profile{}, ErrAuthority
	}
	return p, err
}

// ReadCalendar freezes a window only after rechecking authority. A cursor can
// replay an issued page but cannot jump over unissued pages to claim completion.
func (s *Service) ReadCalendar(ctx context.Context, installation int, r *calendar.CalendarRequest) (*calendar.CalendarPage, error) {
	start, end, err := calendarWindow(r)
	if err != nil {
		return nil, err
	}
	if s == nil || s.Pool == nil || s.calendarSource() == nil {
		return nil, ErrAuthority
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	info, err := authority(ctx, tx, installation, grantShare)
	if err != nil {
		return nil, err
	}
	p, err := calendarProfile(ctx, tx, info.Scope, r.AccountId, r.ProfileId)
	if err != nil {
		return nil, err
	}
	digest, err := s.calendarSource().Authority(ctx, p)
	if err != nil {
		return nil, err
	}
	if digest == "" {
		return nil, ErrAuthority
	}
	var snapshot string
	var revision uint64
	var pageSize, nextOffset int
	var raw []byte
	var storedDigest string
	var expires time.Time
	var accountRevision, profileRevision uint64
	if r.SnapshotId == "" {
		if r.Cursor != "" {
			return nil, ErrSnapshot
		}
		events, err := s.calendarSource().Events(ctx, p, start, end)
		if err != nil {
			return nil, err
		}
		if len(events) > calendarEventLimit {
			return nil, ErrSnapshot
		}
		seen := make(map[string]bool, len(events))
		for _, event := range events {
			if event == nil || event.EventId == "" || len(event.EventId) > 256 || event.ContentId == "" || seen[event.EventId] {
				return nil, ErrSnapshot
			}
			if _, err := time.Parse("2006-01-02", event.Date); err != nil {
				return nil, ErrSnapshot
			}
			eventDay := event.Date
			if event.AirAt != nil {
				if event.AirAt.CheckValid() != nil {
					return nil, ErrSnapshot
				}
				eventDay = event.AirAt.AsTime().UTC().Format("2006-01-02")
			}
			if eventDay < r.Start || eventDay > r.End {
				return nil, ErrSnapshot
			}
			seen[event.EventId] = true
		}
		current, err := s.calendarSource().Authority(ctx, p)
		if err != nil {
			return nil, err
		}
		if current != digest {
			return nil, ErrAuthority
		}
		raw, err = calendarPayload(events)
		if err != nil {
			return nil, err
		}
		if len(raw) > 8<<20 {
			return nil, ErrSnapshot
		}
		snapshot = uuid.NewString()
		pageSize = int(r.Limit)
		storedDigest = calendarDigest(digest+":"+r.Start+":"+r.End, raw)
		accountRevision = p.AccountRevision
		profileRevision = p.Revision
		// Database time is the lease authority across host nodes.
		err = tx.QueryRow(ctx, `INSERT INTO bloem_managed_calendar_snapshots(id,installation_id,account_id,profile_id,account_revision,profile_revision,start_date,end_date,visibility_digest,payload,page_size) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING revision,expires_at`, snapshot, installation, p.AccountID, p.ProfileID, p.AccountRevision, p.Revision, start, end, storedDigest, raw, pageSize).Scan(&revision, &expires)
		if err != nil {
			return nil, err
		}
	} else {
		if _, err = uuid.Parse(r.SnapshotId); err != nil {
			return nil, ErrSnapshot
		}
		snapshot = r.SnapshotId
		err = tx.QueryRow(ctx, `SELECT revision,account_revision,profile_revision,visibility_digest,payload,page_size,next_offset,expires_at FROM bloem_managed_calendar_snapshots WHERE id=$1 AND installation_id=$2 AND account_id=$3 AND profile_id=$4 AND start_date=$5 AND end_date=$6 AND expires_at>clock_timestamp() FOR UPDATE`, snapshot, installation, p.AccountID, p.ProfileID, start, end).Scan(&revision, &accountRevision, &profileRevision, &storedDigest, &raw, &pageSize, &nextOffset, &expires)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSnapshot
	}
	if err != nil {
		return nil, err
	}
	if accountRevision != p.AccountRevision || profileRevision != p.Revision {
		return nil, ErrAuthority
	}
	if pageSize != int(r.Limit) {
		return nil, ErrSnapshot
	}
	offset := 0
	if r.Cursor != "" {
		offset, err = strconv.Atoi(r.Cursor)
		if err != nil || offset <= 0 || strconv.Itoa(offset) != r.Cursor || offset > nextOffset || offset%pageSize != 0 {
			return nil, ErrSnapshot
		}
	}
	data := new(calendar.CalendarPage)
	if err = proto.Unmarshal(raw, data); err != nil {
		return nil, err
	}
	if offset > len(data.Events) || offset == len(data.Events) && offset > 0 {
		return nil, ErrSnapshot
	}
	stop := min(offset+pageSize, len(data.Events))
	out := &calendar.CalendarPage{InstanceId: info.InstanceID, AccountId: p.AccountID, ProfileId: p.ProfileID, SnapshotId: snapshot, Complete: stop == len(data.Events), VisibilityDigest: storedDigest, ExpiresAt: timestamppb.New(expires), Events: data.Events[offset:stop], Start: r.Start, End: r.End, Revision: revision}
	if !out.Complete {
		out.NextCursor = strconv.Itoa(stop)
	}
	currentEvents, err := s.calendarSource().Events(ctx, p, start, end)
	if err != nil {
		return nil, err
	}
	currentPayload, err := calendarPayload(currentEvents)
	if err != nil {
		return nil, err
	}
	current, err := s.calendarSource().Authority(ctx, p)
	if err != nil {
		return nil, err
	}
	if current != digest || calendarDigest(current+":"+r.Start+":"+r.End, currentPayload) != storedDigest || !bytes.Equal(currentPayload, raw) {
		return nil, ErrAuthority
	}
	tag, err := tx.Exec(ctx, `UPDATE bloem_managed_calendar_snapshots SET next_offset=GREATEST(next_offset,$2) WHERE id=$1 AND expires_at>clock_timestamp()`, snapshot, stop)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != 1 {
		return nil, ErrSnapshot
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) CheckCalendarAuthority(ctx context.Context, installation int, r *calendar.CalendarAuthorityRequest) (*calendar.CalendarAuthorityResponse, error) {
	if s == nil || s.Pool == nil || s.calendarSource() == nil {
		return nil, ErrAuthority
	}
	if r == nil || len(r.Profiles) < 1 || len(r.Profiles) > 100 {
		return nil, ErrSnapshot
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	info, err := authority(ctx, tx, installation, grantShare)
	if err != nil {
		return nil, err
	}
	out := &calendar.CalendarAuthorityResponse{InstanceId: info.InstanceID}
	seen := map[string]bool{}
	for _, in := range r.Profiles {
		if in == nil || !positiveID(in.AccountId) || in.ProfileId == "" || strings.TrimSpace(in.VisibilityDigest) == "" || len(in.VisibilityDigest) > 256 {
			return nil, ErrSnapshot
		}
		key := in.AccountId + ":" + in.ProfileId + ":" + in.VisibilityDigest
		if seen[key] {
			return nil, ErrSnapshot
		}
		seen[key] = true
		p, err := calendarProfile(ctx, tx, info.Scope, in.AccountId, in.ProfileId)
		if errors.Is(err, ErrAuthority) {
			continue
		}
		if err != nil {
			return nil, err
		}
		digest, err := s.calendarSource().Authority(ctx, p)
		if errors.Is(err, ErrAuthority) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var snapshot string
		var start, end time.Time
		var payload []byte
		var accountRevision, profileRevision uint64
		err = tx.QueryRow(ctx, `SELECT id::text,start_date,end_date,payload,account_revision,profile_revision FROM bloem_managed_calendar_snapshots WHERE installation_id=$1 AND account_id=$2 AND profile_id=$3 AND visibility_digest=$4 ORDER BY revision DESC LIMIT 1 FOR UPDATE`, installation, p.AccountID, p.ProfileID, in.VisibilityDigest).Scan(&snapshot, &start, &end, &payload, &accountRevision, &profileRevision)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if p.AccountRevision != accountRevision || p.Revision != profileRevision {
			continue
		}
		events, err := s.calendarSource().Events(ctx, p, start, end)
		if err != nil {
			return nil, err
		}
		currentPayload, err := calendarPayload(events)
		if err != nil {
			return nil, err
		}
		current, err := s.calendarSource().Authority(ctx, p)
		if err != nil {
			return nil, err
		}
		if current != digest || calendarDigest(current+":"+start.Format("2006-01-02")+":"+end.Format("2006-01-02"), currentPayload) != in.VisibilityDigest || !bytes.Equal(currentPayload, payload) {
			continue
		}
		var expires time.Time
		if err = tx.QueryRow(ctx, `UPDATE bloem_managed_calendar_snapshots SET expires_at=clock_timestamp()+interval '120 seconds' WHERE id=$1 RETURNING expires_at`, snapshot).Scan(&expires); err != nil {
			return nil, err
		}
		out.Authorized = append(out.Authorized, &calendar.CalendarAuthorized{Profile: &calendar.CalendarAuthority{AccountId: in.AccountId, ProfileId: in.ProfileId, VisibilityDigest: in.VisibilityDigest}, ExpiresAt: timestamppb.New(expires)})
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
