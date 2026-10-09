package managedtracking

import (
	"context"
	managedv1 "github.com/Silo-Server/silo-server/internal/managedtracking/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"net"
	"testing"
)

func TestNativeBrokerRejectsUnrelatedPluginAndCallerScope(t *testing.T) {
	s, id, _ := enrollmentFixture(t)
	for _, tc := range []struct {
		plugin string
		id     int
		code   codes.Code
	}{{"unrelated", id, codes.Unimplemented}, {"bloem.pastime", -1, codes.PermissionDenied}, {"bloem.pastime", id, codes.OK}} {
		t.Run(tc.plugin+tc.code.String(), func(t *testing.T) {
			listener := bufconn.Listen(1 << 20)
			server := grpc.NewServer()
			s.RegisterBroker(server, tc.plugin, tc.id)
			go server.Serve(listener)
			t.Cleanup(server.Stop)
			conn, err := grpc.NewClient("passthrough:///fixture", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.Close() })
			out, err := managedv1.NewManagedTrackingHostClient(conn).GetInfo(t.Context(), &managedv1.InfoRequest{})
			if status.Code(err) != tc.code {
				t.Fatalf("code=%v error=%v", status.Code(err), err)
			}
			if tc.code == codes.OK && (out.GetCapability() != "bloem.managed_tracking.v1" || out.GetCredentialGeneration() == 0) {
				t.Fatal("invalid advertised authority")
			}
		})
	}
}
