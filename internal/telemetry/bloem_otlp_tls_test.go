package telemetry

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	collector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

// Exercise the production exporter constructor, not an explicitly configured
// TLS client: the dependency advisory concerns ignored environment certificates.
func TestBloemOTLPLogExporterHonorsEnvironmentMTLS(t *testing.T) {
	ca, caKey, caPEM, _ := bloemTelemetryCertificate(t, nil, nil, true, false)
	_, _, serverPEM, serverKey := bloemTelemetryCertificate(t, ca, caKey, false, false)
	_, _, clientPEM, clientKey := bloemTelemetryCertificate(t, ca, caKey, false, true)
	_, _, otherCAPEM, _ := bloemTelemetryCertificate(t, nil, nil, true, false)
	serverCert, err := tls.X509KeyPair(serverPEM, serverKey)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("invalid test CA")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverCert},
		ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots,
	})))
	accepted := make(chan bool, 4)
	collector.RegisterLogsServiceServer(server, &bloemTLSLogCollector{accepted: accepted})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	t.Cleanup(func() { _ = listener.Close() })

	writePEM := func(name string, data []byte) string {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	caPath := writePEM("ca.pem", caPEM)
	wrongCAPath := writePEM("other-ca.pem", otherCAPEM)
	certPath := writePEM("client.pem", clientPEM)
	keyPath := writePEM("client-key.pem", clientKey)
	for _, scope := range []string{"OTEL_EXPORTER_OTLP_", "OTEL_EXPORTER_OTLP_LOGS_"} {
		t.Run(scope, func(t *testing.T) {
			for _, prefix := range []string{"OTEL_EXPORTER_OTLP_", "OTEL_EXPORTER_OTLP_LOGS_"} {
				for _, key := range []string{"ENDPOINT", "INSECURE", "CERTIFICATE", "CLIENT_CERTIFICATE", "CLIENT_KEY", "HEADERS"} {
					t.Setenv(prefix+key, "")
				}
			}
			t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "https://"+listener.Addr().String())
			t.Setenv(scope+"CERTIFICATE", caPath)
			t.Setenv(scope+"CLIENT_CERTIFICATE", certPath)
			t.Setenv(scope+"CLIENT_KEY", keyPath)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			exporter, err := newLogExporter(ctx, Config{LogsProtocol: ProtocolGRPC})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = exporter.Shutdown(context.Background()) })
			if err := exporter.Export(ctx, []sdklog.Record{{}}); err != nil {
				t.Fatalf("export with environment mTLS: %v", err)
			}
			select {
			case valid := <-accepted:
				if !valid {
					t.Fatal("collector did not receive a verified client certificate and log record")
				}
			case <-ctx.Done():
				t.Fatal("collector did not receive the exported record")
			}
			// Supplying an unrelated trust root must not fall back to insecure
			// transport. Keep the valid client identity so this checks server trust.
			t.Setenv(scope+"CERTIFICATE", wrongCAPath)
			badCtx, badCancel := context.WithTimeout(t.Context(), time.Second)
			defer badCancel()
			badExporter, err := newLogExporter(badCtx, Config{LogsProtocol: ProtocolGRPC})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = badExporter.Shutdown(context.Background()) })
			if err := badExporter.Export(badCtx, []sdklog.Record{{}}); err == nil {
				t.Fatal("export accepted an unrelated server trust root")
			}
		})
	}
}

type bloemTLSLogCollector struct {
	collector.UnimplementedLogsServiceServer
	accepted chan bool
}

func (s *bloemTLSLogCollector) Export(ctx context.Context, req *collector.ExportLogsServiceRequest) (*collector.ExportLogsServiceResponse, error) {
	p, ok := peer.FromContext(ctx)
	valid := false
	if ok {
		info, ok := p.AuthInfo.(credentials.TLSInfo)
		valid = ok && len(info.State.VerifiedChains) > 0 && len(req.ResourceLogs) > 0
	}
	s.accepted <- valid
	return &collector.ExportLogsServiceResponse{}, nil
}

func bloemTelemetryCertificate(t *testing.T, parent *x509.Certificate, signer *ecdsa.PrivateKey, ca, client bool) (*x509.Certificate, *ecdsa.PrivateKey, []byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "bloem-telemetry-fixture"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		BasicConstraintsValid: true, IsCA: ca, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	if ca {
		template.KeyUsage |= x509.KeyUsageCertSign
		parent, signer = template, key
	} else if client {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	} else {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}
