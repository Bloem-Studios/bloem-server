package watchtogether

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const defaultRoomOwnerLease = 15 * time.Second

type ownerChangePayload struct {
	NodeID     string    `json:"node_id"`
	LeaseUntil time.Time `json:"lease_until"`
}

type presencePayload struct {
	UserID        int    `json:"user_id"`
	ProfileID     string `json:"profile_id"`
	DisplayName   string `json:"display_name"`
	IngressNode   string `json:"ingress_node"`
	SessionID     string `json:"session_id,omitempty"`
	IsReady       bool   `json:"is_ready,omitempty"`
	IsBuffering   bool   `json:"is_buffering,omitempty"`
	IgnoreWait    bool   `json:"ignore_wait,omitempty"`
	ExplicitLeave bool   `json:"explicit_leave,omitempty"`
}

type relayedFramePayload struct {
	Body json.RawMessage `json:"body"`
}

type relayedConnectionRequest struct {
	UserID    int               `json:"user_id"`
	ProfileID string            `json:"profile_id"`
	SessionID string            `json:"session_id,omitempty"`
	Transport *TransportRequest `json:"transport,omitempty"`
	State     *StateReport      `json:"state,omitempty"`
	PingMS    int64             `json:"ping_ms,omitempty"`
}

type relayedRoomRequest struct {
	UserID    int                `json:"user_id"`
	ProfileID string             `json:"profile_id"`
	Policy    GuestControlPolicy `json:"policy,omitempty"`
	Selection *SelectItemInput   `json:"selection,omitempty"`
	ViaVote   bool               `json:"via_vote,omitempty"`
}

type relayedRoomResponse struct {
	Snapshot *Snapshot `json:"snapshot,omitempty"`
	Error    string    `json:"error,omitempty"`
}

type relayedRoomConnection struct {
	service     *Service
	roomID      string
	memberKey   string
	ingressNode string
	generation  int64
}

func (connection *relayedRoomConnection) WriteJSON(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(relayedFramePayload{Body: encoded})
	if err != nil {
		return err
	}
	return connection.service.relay.Publish(context.Background(), Command{
		RoomID: connection.roomID, Generation: connection.generation,
		CommandID: uuid.NewString(), Kind: "frame", OriginNodeID: connection.service.nodeID,
		TargetNodeID: connection.ingressNode, MemberKey: connection.memberKey, Payload: payload,
	})
}

func (*relayedRoomConnection) Close() error { return nil }

// SetDistributedRuntime enables generation-fenced room ownership and the
// cross-replica command transport. It is deliberately explicit: clustered
// production must never silently fall back to process-local authority.
func (s *Service) SetDistributedRuntime(ctx context.Context, nodeID string, owner RoomOwner, relay RoomRelay) error {
	if s == nil || ctx == nil || nodeID == "" || owner == nil || relay == nil {
		return fmt.Errorf("configure watch together distributed runtime: %w", ErrRoomOwnershipInvalid)
	}
	s.distributedMu.Lock()
	if s.relaySubscription != nil {
		_ = s.relaySubscription.Close()
		s.relaySubscription = nil
	}
	s.nodeID = nodeID
	s.owner = owner
	s.relay = relay
	s.ownerLease = defaultRoomOwnerLease
	subscription, err := relay.Subscribe(ctx, s.handleRelayCommand)
	if err != nil {
		s.nodeID = ""
		s.owner = nil
		s.relay = nil
		s.ownerLease = 0
		s.distributedMu.Unlock()
		return fmt.Errorf("subscribe watch together relay: %w", err)
	}
	s.relaySubscription = subscription
	s.distributedMu.Unlock()
	go s.runOwnerRenewal(ctx)
	return nil
}

func (s *Service) ensureRoomOwnership(ctx context.Context, live *liveRoom) (Ownership, error) {
	if s.owner == nil {
		return Ownership{}, nil
	}
	s.distributedMu.Lock()
	defer s.distributedMu.Unlock()
	s.mu.Lock()
	current := live.ownership
	s.mu.Unlock()
	if current.Generation > 0 && current.LeaseUntil.After(s.now()) {
		return current, nil
	}
	leaseUntil := s.now().Add(s.ownerLease)
	ownership, err := s.owner.Acquire(ctx, live.room.ID, s.nodeID, leaseUntil)
	if errors.Is(err, ErrRoomOwned) {
		ownership, err = s.owner.Current(ctx, live.room.ID)
	}
	if err != nil {
		return Ownership{}, err
	}
	s.mu.Lock()
	live.ownership = ownership
	s.mu.Unlock()
	if ownership.NodeID == s.nodeID {
		s.publishOwnerChange(ctx, ownership)
	}
	return ownership, nil
}

func (s *Service) publishOwnerChange(ctx context.Context, ownership Ownership) {
	payload, _ := json.Marshal(ownerChangePayload{NodeID: ownership.NodeID, LeaseUntil: ownership.LeaseUntil})
	_ = s.relay.Publish(ctx, Command{
		RoomID: ownership.RoomID, Generation: ownership.Generation,
		CommandID: uuid.NewString(), Kind: "owner_changed", OriginNodeID: s.nodeID, Payload: payload,
	})
}

func (s *Service) runOwnerRenewal(ctx context.Context) {
	ticker := time.NewTicker(defaultRoomOwnerLease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.janitorStop:
			return
		case <-ticker.C:
			s.renewOwnedRooms(ctx)
		}
	}
}

func (s *Service) renewOwnedRooms(ctx context.Context) {
	s.mu.Lock()
	liveRooms := make([]*liveRoom, 0, len(s.rooms))
	for _, live := range s.rooms {
		if live.ownership.NodeID == s.nodeID && live.ownership.Generation > 0 {
			liveRooms = append(liveRooms, live)
		}
	}
	s.mu.Unlock()
	for _, live := range liveRooms {
		s.distributedMu.Lock()
		s.mu.Lock()
		current := live.ownership
		s.mu.Unlock()
		renewed, err := s.owner.Renew(ctx, current, s.now().Add(s.ownerLease))
		if err == nil {
			s.mu.Lock()
			if live.ownership.Generation == current.Generation {
				live.ownership = renewed
			}
			s.mu.Unlock()
		}
		s.distributedMu.Unlock()
	}
}

func (s *Service) handleRelayCommand(command Command) {
	if s == nil || command.RoomID == "" || command.Generation <= 0 {
		return
	}
	if command.TargetNodeID != "" && command.TargetNodeID != s.nodeID {
		return
	}
	if command.TargetNodeID != "" && command.Kind != "owner_changed" {
		claimed, err := s.relay.Claim(context.Background(), command.RoomID, command.Generation, command.CommandID, 10*time.Minute)
		if err != nil || !claimed {
			return
		}
	}
	if command.Kind == "frame" {
		s.deliverRelayedFrame(command)
		return
	}
	if command.Kind == "room_response" {
		s.deliverRoomResponse(command)
		return
	}
	if command.Kind == "presence_connected" || command.Kind == "presence_disconnected" {
		s.handleRelayedPresence(command)
		return
	}
	if command.Kind == "attach_session" || command.Kind == "transport_request" || command.Kind == "state_report" || command.Kind == "ready" || command.Kind == "buffering" || command.Kind == "ping" {
		s.handleRelayedConnectionRequest(command)
		return
	}
	if command.Kind == "update_policy" || command.Kind == "select_item" || command.Kind == "close_room" {
		s.handleRelayedRoomRequest(command)
		return
	}
	if command.Kind == "suggestions_refresh" {
		s.handleSuggestionRefresh(command)
		return
	}
	s.mu.Lock()
	live := s.rooms[command.RoomID]
	if live == nil {
		s.mu.Unlock()
		return
	}
	var reannounce map[string]memberState
	if command.Generation >= live.ownership.Generation {
		live.ownership.Generation = command.Generation
		if command.Kind == "owner_changed" {
			var payload ownerChangePayload
			if json.Unmarshal(command.Payload, &payload) == nil {
				live.ownership.NodeID = payload.NodeID
				live.ownership.LeaseUntil = payload.LeaseUntil
				if payload.NodeID != s.nodeID {
					reannounce = make(map[string]memberState)
					for memberKey, member := range live.members {
						if member == nil || member.connection == nil {
							continue
						}
						if _, remote := member.connection.(*relayedRoomConnection); remote {
							continue
						}
						reannounce[memberKey] = *member
					}
				}
			}
		}
	}
	s.mu.Unlock()
	for memberKey, member := range reannounce {
		_ = s.publishPresence(context.Background(), live, memberKey, &member, true)
	}
}

func (s *Service) dispatchSuggestionUpdate(ctx context.Context, live *liveRoom, suggestions []Suggestion) {
	if live == nil {
		return
	}
	s.mu.Lock()
	ownership := live.ownership
	roomID := live.room.ID
	if s.relay == nil || ownership.NodeID == "" || ownership.NodeID == s.nodeID {
		dispatches := s.prepareSuggestionDispatchesLocked(live, suggestions)
		s.mu.Unlock()
		s.runDispatches(dispatches)
		return
	}
	s.mu.Unlock()
	_ = s.relay.Publish(ctx, Command{
		RoomID: roomID, Generation: ownership.Generation, CommandID: uuid.NewString(), Kind: "suggestions_refresh",
		OriginNodeID: s.nodeID, TargetNodeID: ownership.NodeID,
	})
}

func (s *Service) handleSuggestionRefresh(command Command) {
	s.mu.Lock()
	live := s.rooms[command.RoomID]
	isOwner := live != nil && live.ownership.NodeID == s.nodeID && live.ownership.Generation == command.Generation
	s.mu.Unlock()
	if !isOwner || s.suggestions == nil {
		return
	}
	suggestions, err := s.suggestions.ListSuggestions(context.Background(), command.RoomID, "")
	if err != nil {
		return
	}
	s.dispatchSuggestionUpdate(context.Background(), live, suggestions)
}

func (s *Service) forwardRoomRequest(ctx context.Context, live *liveRoom, kind string, request relayedRoomRequest) (bool, Snapshot, error) {
	s.mu.Lock()
	ownership := live.ownership
	roomID := live.room.ID
	s.mu.Unlock()
	if s.relay == nil || ownership.NodeID == "" || ownership.NodeID == s.nodeID {
		return false, Snapshot{}, nil
	}
	requestID := uuid.NewString()
	responseChannel := make(chan relayedRoomResponse, 1)
	s.pendingMu.Lock()
	s.pendingRelay[requestID] = responseChannel
	s.pendingMu.Unlock()
	defer func() {
		s.pendingMu.Lock()
		delete(s.pendingRelay, requestID)
		s.pendingMu.Unlock()
	}()
	payload, err := json.Marshal(request)
	if err != nil {
		return true, Snapshot{}, err
	}
	if err := s.relay.Publish(ctx, Command{
		RoomID: roomID, Generation: ownership.Generation, CommandID: requestID, Kind: kind,
		OriginNodeID: s.nodeID, TargetNodeID: ownership.NodeID, Payload: payload,
	}); err != nil {
		return true, Snapshot{}, err
	}
	select {
	case response := <-responseChannel:
		if response.Error != "" {
			return true, Snapshot{}, decodeRelayedRoomError(response.Error)
		}
		if response.Snapshot != nil {
			return true, *response.Snapshot, nil
		}
		return true, Snapshot{}, nil
	case <-ctx.Done():
		return true, Snapshot{}, ctx.Err()
	case <-time.After(5 * time.Second):
		return true, Snapshot{}, ErrRoomRelayUnavailable
	}
}

func (s *Service) handleRelayedRoomRequest(command Command) {
	var request relayedRoomRequest
	if json.Unmarshal(command.Payload, &request) != nil || request.UserID <= 0 || request.ProfileID == "" {
		return
	}
	s.mu.Lock()
	live := s.rooms[command.RoomID]
	isOwner := live != nil && live.ownership.NodeID == s.nodeID && live.ownership.Generation == command.Generation
	s.mu.Unlock()
	if !isOwner {
		return
	}
	var snapshot Snapshot
	var err error
	switch command.Kind {
	case "update_policy":
		snapshot, err = s.UpdatePolicy(context.Background(), command.RoomID, request.UserID, request.ProfileID, request.Policy)
	case "select_item":
		if request.Selection == nil {
			err = ErrInvalidSelection
		} else {
			snapshot, err = s.selectItem(context.Background(), command.RoomID, request.UserID, request.ProfileID, *request.Selection, request.ViaVote)
		}
	case "close_room":
		err = s.CloseRoom(context.Background(), command.RoomID, request.UserID, request.ProfileID)
	}
	response := relayedRoomResponse{}
	if err != nil {
		response.Error = encodeRelayedRoomError(err)
	} else if command.Kind != "close_room" {
		response.Snapshot = &snapshot
	}
	payload, _ := json.Marshal(response)
	_ = s.relay.Publish(context.Background(), Command{
		RoomID: command.RoomID, Generation: command.Generation, CommandID: uuid.NewString(), Kind: "room_response",
		OriginNodeID: s.nodeID, TargetNodeID: command.OriginNodeID, CorrelationID: command.CommandID, Payload: payload,
	})
}

func (s *Service) deliverRoomResponse(command Command) {
	var response relayedRoomResponse
	if command.CorrelationID == "" || json.Unmarshal(command.Payload, &response) != nil {
		return
	}
	s.pendingMu.Lock()
	channel := s.pendingRelay[command.CorrelationID]
	s.pendingMu.Unlock()
	if channel != nil {
		select {
		case channel <- response:
		default:
		}
	}
}

func encodeRelayedRoomError(err error) string {
	for code, target := range map[string]error{
		"forbidden": ErrRoomForbidden, "closed": ErrRoomClosed, "invalid_selection": ErrInvalidSelection,
		"transport_not_allowed": ErrTransportNotAllowed, "vote_selection": ErrVoteRoomSelection,
		"not_attached": ErrConnectionNotAttached, "session_mismatch": ErrSessionMismatch,
	} {
		if errors.Is(err, target) {
			return code
		}
	}
	return "internal"
}

func decodeRelayedRoomError(code string) error {
	switch code {
	case "forbidden":
		return ErrRoomForbidden
	case "closed":
		return ErrRoomClosed
	case "invalid_selection":
		return ErrInvalidSelection
	case "transport_not_allowed":
		return ErrTransportNotAllowed
	case "vote_selection":
		return ErrVoteRoomSelection
	case "not_attached":
		return ErrConnectionNotAttached
	case "session_mismatch":
		return ErrSessionMismatch
	default:
		return ErrRoomRelayUnavailable
	}
}

func (s *Service) forwardConnectionRequest(ctx context.Context, live *liveRoom, reg *Registration, kind string, request relayedConnectionRequest) (bool, Snapshot, error) {
	s.mu.Lock()
	ownership := live.ownership
	roomID := live.room.ID
	if s.relay == nil || ownership.NodeID == "" || ownership.NodeID == s.nodeID {
		s.mu.Unlock()
		return false, Snapshot{}, nil
	}
	member := live.members[reg.memberKey]
	if member == nil || member.connection == nil || member.connection != reg.connection {
		s.mu.Unlock()
		return true, Snapshot{}, ErrRoomForbidden
	}
	if kind == "attach_session" {
		member.sessionID = request.SessionID
		member.isReady = false
		member.ignoreWait = false
		member.isBuffering = live.room.Phase == RoomPhasePlaying
	}
	snapshot := s.buildSnapshotLocked(live, request.UserID, request.ProfileID)
	s.mu.Unlock()
	requestID := uuid.NewString()
	responseChannel := make(chan relayedRoomResponse, 1)
	s.pendingMu.Lock()
	s.pendingRelay[requestID] = responseChannel
	s.pendingMu.Unlock()
	defer func() {
		s.pendingMu.Lock()
		delete(s.pendingRelay, requestID)
		s.pendingMu.Unlock()
	}()
	payload, err := json.Marshal(request)
	if err != nil {
		return true, Snapshot{}, err
	}
	err = s.relay.Publish(ctx, Command{
		RoomID: roomID, Generation: ownership.Generation, CommandID: requestID, Kind: kind,
		OriginNodeID: s.nodeID, TargetNodeID: ownership.NodeID, MemberKey: reg.memberKey, Payload: payload,
	})
	if err != nil {
		return true, Snapshot{}, err
	}
	select {
	case response := <-responseChannel:
		if response.Error != "" {
			return true, Snapshot{}, decodeRelayedRoomError(response.Error)
		}
		if response.Snapshot != nil {
			return true, *response.Snapshot, nil
		}
		return true, snapshot, nil
	case <-ctx.Done():
		return true, Snapshot{}, ctx.Err()
	case <-time.After(5 * time.Second):
		return true, Snapshot{}, ErrRoomRelayUnavailable
	}
}

func (s *Service) handleRelayedConnectionRequest(command Command) {
	var request relayedConnectionRequest
	if json.Unmarshal(command.Payload, &request) != nil || request.UserID <= 0 || request.ProfileID == "" {
		return
	}
	s.mu.Lock()
	live := s.rooms[command.RoomID]
	if live == nil || live.ownership.NodeID != s.nodeID || live.ownership.Generation != command.Generation {
		s.mu.Unlock()
		return
	}
	member := live.members[command.MemberKey]
	if member == nil || member.connection == nil {
		s.mu.Unlock()
		return
	}
	reg := &Registration{roomID: command.RoomID, memberKey: command.MemberKey, connection: member.connection}
	s.mu.Unlock()
	var snapshot Snapshot
	var err error
	switch command.Kind {
	case "attach_session":
		snapshot, err = s.AttachSessionForConnection(context.Background(), reg, request.UserID, request.ProfileID, request.SessionID)
	case "transport_request":
		if request.Transport != nil {
			snapshot, err = s.HandleTransportRequestForConnection(context.Background(), reg, request.UserID, request.ProfileID, *request.Transport)
		}
	case "state_report":
		if request.State != nil {
			snapshot, err = s.HandleStateReportForConnection(context.Background(), reg, request.UserID, request.ProfileID, *request.State)
		}
	case "ready":
		if request.State != nil {
			snapshot, err = s.HandleReadyForConnection(context.Background(), reg, request.UserID, request.ProfileID, *request.State)
		}
	case "buffering":
		if request.State != nil {
			snapshot, err = s.HandleBufferingForConnection(context.Background(), reg, request.UserID, request.ProfileID, *request.State)
		}
	case "ping":
		err = s.HandlePingForConnection(context.Background(), reg, request.UserID, request.ProfileID, request.PingMS)
	}
	response := relayedRoomResponse{Snapshot: &snapshot}
	if err != nil {
		response.Snapshot = nil
		response.Error = encodeRelayedRoomError(err)
	}
	payload, _ := json.Marshal(response)
	_ = s.relay.Publish(context.Background(), Command{
		RoomID: command.RoomID, Generation: command.Generation, CommandID: uuid.NewString(), Kind: "room_response",
		OriginNodeID: s.nodeID, TargetNodeID: command.OriginNodeID, CorrelationID: command.CommandID, Payload: payload,
	})
}

func (s *Service) publishPresence(ctx context.Context, live *liveRoom, memberKey string, member *memberState, connected bool, explicitLeave ...bool) error {
	s.mu.Lock()
	ownership := live.ownership
	roomID := live.room.ID
	memberCopy := *member
	s.mu.Unlock()
	if s.relay == nil || ownership.NodeID == "" || ownership.NodeID == s.nodeID {
		return nil
	}
	payload, err := json.Marshal(presencePayload{
		UserID: memberCopy.userID, ProfileID: memberCopy.profileID, DisplayName: memberCopy.displayName, IngressNode: s.nodeID,
		SessionID: memberCopy.sessionID, IsReady: memberCopy.isReady, IsBuffering: memberCopy.isBuffering, IgnoreWait: memberCopy.ignoreWait,
		ExplicitLeave: len(explicitLeave) > 0 && explicitLeave[0],
	})
	if err != nil {
		return err
	}
	kind := "presence_connected"
	if !connected {
		kind = "presence_disconnected"
	}
	return s.relay.Publish(ctx, Command{
		RoomID: roomID, Generation: ownership.Generation, CommandID: uuid.NewString(), Kind: kind,
		OriginNodeID: s.nodeID, TargetNodeID: ownership.NodeID, MemberKey: memberKey, Payload: payload,
	})
}

func (s *Service) handleRelayedPresence(command Command) {
	var presence presencePayload
	if json.Unmarshal(command.Payload, &presence) != nil || presence.UserID <= 0 || presence.ProfileID == "" || presence.IngressNode == "" {
		return
	}
	_, live, err := s.getOrLoadLiveRoom(context.Background(), command.RoomID)
	if err != nil {
		return
	}
	s.mu.Lock()
	if live.ownership.NodeID != s.nodeID || live.ownership.Generation != command.Generation {
		s.mu.Unlock()
		return
	}
	if command.Kind == "presence_disconnected" {
		isHost := presence.UserID == live.room.HostUserID && presence.ProfileID == live.room.HostProfileID
		delete(live.members, command.MemberKey)
		dispatches, commands := s.maybeResumeFromWaitingLocked(context.Background(), live, false)
		if dispatches == nil {
			dispatches = s.prepareSnapshotDispatchesLocked(live)
		}
		if isHost && !presence.ExplicitLeave {
			if live.hostCloseTimer != nil {
				live.hostCloseTimer.Stop()
			}
			roomID := live.room.ID
			ownerGeneration := live.ownership.Generation
			live.hostCloseTimer = time.AfterFunc(s.hostDisconnectTTL, func() {
				s.closeIfHostStillDisconnected(roomID, presence.UserID, presence.ProfileID, ownerGeneration)
			})
		}
		s.mu.Unlock()
		s.runDispatches(dispatches)
		s.runCommandDispatches(commands)
		if isHost && presence.ExplicitLeave {
			_ = s.CloseRoom(context.Background(), command.RoomID, presence.UserID, presence.ProfileID)
		}
		return
	}
	member := live.members[command.MemberKey]
	if member == nil {
		member = &memberState{userID: presence.UserID, profileID: presence.ProfileID}
		live.members[command.MemberKey] = member
	}
	member.displayName = presence.DisplayName
	member.sessionID = presence.SessionID
	member.isReady = presence.IsReady
	member.isBuffering = presence.IsBuffering
	member.ignoreWait = presence.IgnoreWait
	member.connection = &relayedRoomConnection{
		service: s, roomID: command.RoomID, memberKey: command.MemberKey,
		ingressNode: presence.IngressNode, generation: command.Generation,
	}
	dispatches := s.prepareSnapshotDispatchesLocked(live)
	s.mu.Unlock()
	s.runDispatches(dispatches)
}

func (s *Service) deliverRelayedFrame(command Command) {
	var frame relayedFramePayload
	if json.Unmarshal(command.Payload, &frame) != nil || len(frame.Body) == 0 {
		return
	}
	var body any
	if json.Unmarshal(frame.Body, &body) != nil {
		return
	}
	s.mu.Lock()
	live := s.rooms[command.RoomID]
	if live == nil || command.Generation < live.ownership.Generation {
		s.mu.Unlock()
		return
	}
	member := live.members[command.MemberKey]
	if member == nil || member.connection == nil {
		s.mu.Unlock()
		return
	}
	connection := member.connection
	s.mu.Unlock()
	_ = connection.WriteJSON(body)
}
