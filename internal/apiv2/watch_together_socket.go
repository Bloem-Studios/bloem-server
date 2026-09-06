package apiv2

import (
	"context"
	"net/http"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/watchtogether"
	"github.com/danielgtaylor/huma/v2"
)

const roomSocketPathParameter = "path"

type WatchTogetherSocketService interface {
	MintRoomSocket(context.Context, string, string, evt.SocketIdentity) (string, time.Time, error)
	ServeHTTP(http.ResponseWriter, *http.Request)
}
type WatchTogetherSocketTicketInput struct {
	RoomID    string `path:"room_id"`
	RoomToken string `header:"X-Room-Token" maxLength:"4096"`
}

func registerWatchTogetherSocket(reg *Registry) {
	op := Operation{Operation: humaOp(http.MethodPost, Prefix+"/watch-together/rooms/{room_id}/ws-ticket", "createWatchTogetherSocketTicket", "realtime", "Delegate a current login session and original room proof for one room handshake."), Class: ClassProfileScoped, DemoRestricted: true, ServiceBacked: true, RetrySafety: RetrySafetyNaturalIdempotent}
	op.Errors = []int{409}
	Register(reg, op, func(ctx context.Context, in *WatchTogetherSocketTicketInput) (*EventsSocketTicketOutput, error) {
		if reg.deps.WatchTogetherSocket == nil {
			return nil, unavailable("room socket")
		}
		claims := claimsFrom(ctx)
		if claims.TokenType != auth.TokenTypeAccess || claims.SessionID == "" || claims.ExpiresAt == nil || !claims.ExpiresAt.After(time.Now()) {
			return nil, NewProblem(TypePermissionDenied, "A current login session is required.")
		}
		identity := evt.SocketIdentity{UserID: claims.UserID, SessionID: claims.SessionID, Role: claims.Role, ImpersonatorUserID: claims.ImpersonatorUserID, ProfileID: profileFrom(ctx), AccessExpiresAt: claims.ExpiresAt.Time}
		if r := requestFrom(ctx); r != nil {
			identity.ProfileToken = r.Header.Get("X-Profile-Token")
		}
		ticket, expiry, err := reg.deps.WatchTogetherSocket.MintRoomSocket(ctx, in.RoomID, in.RoomToken, identity)
		if err != nil {
			return nil, suggestionProblem(err)
		}
		return &EventsSocketTicketOutput{Body: EventsSocketTicket{Ticket: ticket, ExpiresIn: max(0, int(time.Until(expiry).Seconds())), MaxConnectionSeconds: int(watchtogether.RoomSocketMaxLifetime.Seconds()), Protocol: watchtogether.RoomSocketProtocol}}, nil
	})
	responses := map[string]*huma.Response{}
	for _, status := range []string{"400", "401", "403", "503"} {
		responses[status] = &huma.Response{Description: "Room handshake refused.", Content: map[string]*huma.MediaType{eventsPlainMedia: {Schema: &huma.Schema{Type: huma.TypeString}}}}
	}
	responses["101"] = &huma.Response{Description: "Room connection established.", Headers: map[string]*huma.Param{}}
	for _, header := range []string{eventsConnectionHeader, eventsUpgradeHeader, eventsAcceptHeader, eventsProtocolHeader} {
		responses["101"].Headers[header] = &huma.Param{Schema: &huma.Schema{Type: huma.TypeString}, Description: "WebSocket handshake header."}
	}
	raw := Operation{Operation: huma.Operation{Method: http.MethodGet, Path: Prefix + "/watch-together/rooms/{room_id}/ws", OperationID: "connectWatchTogetherSocket", Tags: []string{"realtime"}, Summary: "Connect a room using a single-use session-bound credential.", Responses: responses}, Class: ClassPublic, ServiceBacked: true}
	raw.Parameters = []*huma.Param{
		{Name: "room_id", In: roomSocketPathParameter, Required: true, Schema: &huma.Schema{Type: huma.TypeString}},
		{Name: eventsProtocolHeader, In: paramInHeader, Required: true, Schema: &huma.Schema{Type: huma.TypeString}, Description: "Offer silo.room.v2 followed by silo.ticket.<single-use-ticket>."},
		{Name: eventsOriginHeader, In: paramInHeader, Schema: &huma.Schema{Type: huma.TypeString}, Description: "Browser origin must match configured public origin."},
	}
	RegisterRaw(reg, RawOperation{Operation: raw, Protocol: eventsRawProtocol, Reason: "Room-bound single-use session proof, Origin and subprotocol checks precede upgrade; connection authority and lifetime are bounded."}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if reg.deps.WatchTogetherSocket == nil {
			http.Error(w, "room socket unavailable", http.StatusServiceUnavailable)
			return
		}
		reg.deps.WatchTogetherSocket.ServeHTTP(w, r)
	}))
}
