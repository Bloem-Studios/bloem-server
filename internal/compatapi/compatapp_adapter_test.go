package compatapi

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/Silo-Server/silo-server/internal/compatapp"
)

// TestCapabilityVocabularyMatchesEnrollmentRegistry pins the two capability
// vocabularies to each other: the OpenAPI contract's slugs (mirrored by
// KnownCapabilities) are canonical, and the enrollment service must grant
// exactly that set minus the implicit "service" capability. Any drift on
// either side fails here before it can 403 a live companion.
func TestCapabilityVocabularyMatchesEnrollmentRegistry(t *testing.T) {
	want := make([]string, 0, len(KnownCapabilities()))
	for _, capability := range KnownCapabilities() {
		if capability == CapabilityService {
			continue
		}
		want = append(want, capability)
	}
	sort.Strings(want)

	got := make([]string, 0, len(compatapp.GrantableCapabilities()))
	for _, capability := range compatapp.GrantableCapabilities() {
		got = append(got, string(capability))
	}
	sort.Strings(got)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("enrollment registry %v must equal contract capabilities minus service %v", got, want)
	}
}

// TestMapCompatAppErrorTranslatesEverySentinel maps each compatapp sentinel
// onto the envelope sentinel its HTTP status derives from, keeping the
// original error reachable for logs and errors.Is.
func TestMapCompatAppErrorTranslatesEverySentinel(t *testing.T) {
	cases := []struct {
		name string
		in   error
		want error
	}{
		{"enrollment denied", compatapp.ErrEnrollmentDenied, ErrInvalidCredentials},
		{"credential invalid", compatapp.ErrCredentialInvalid, ErrInvalidCredentials},
		{"application revoked", compatapp.ErrApplicationRevoked, ErrInvalidCredentials},
		{"application disabled", compatapp.ErrApplicationDisabled, ErrInvalidCredentials},
		{"application not found", compatapp.ErrApplicationNotFound, ErrInvalidCredentials},
		{"peer certificate required", compatapp.ErrPeerCertificateRequired, ErrInvalidCredentials},
		{"peer certificate mismatch", compatapp.ErrPeerCertificateMismatch, ErrInvalidCredentials},
		{"instance already enrolled", compatapp.ErrInstanceAlreadyEnrolled, ErrConflict},
		{"api range unsupported", compatapp.ErrAPIRangeUnsupported, ErrConflict},
		{"kind mismatch", compatapp.ErrKindMismatch, ErrConflict},
		{"unknown kind", compatapp.ErrUnknownKind, ErrInvalid},
		{"unknown capability", compatapp.ErrUnknownCapability, ErrInvalid},
		{"no capabilities", compatapp.ErrNoCapabilities, ErrInvalid},
		{"capability not granted", compatapp.ErrCapabilityNotGranted, ErrInvalid},
		{"invalid enrollment request", compatapp.ErrInvalidEnrollmentRequest, ErrInvalid},
		{"invalid health status", compatapp.ErrInvalidHealthStatus, ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapped := mapCompatAppError(tc.in)
			if !errors.Is(mapped, tc.want) {
				t.Fatalf("mapCompatAppError(%v) = %v, want errors.Is %v", tc.in, mapped, tc.want)
			}
			if !errors.Is(mapped, tc.in) {
				t.Fatalf("the original sentinel must stay reachable, got %v", mapped)
			}
		})
	}

	if mapCompatAppError(nil) != nil {
		t.Fatal("nil must map to nil")
	}
	unknown := errors.New("connection reset")
	if mapped := mapCompatAppError(unknown); !errors.Is(mapped, unknown) ||
		errors.Is(mapped, ErrInvalid) || errors.Is(mapped, ErrConflict) || errors.Is(mapped, ErrInvalidCredentials) {
		t.Fatalf("an unmapped error must pass through untranslated, got %v", mapped)
	}
}

// The adapter's translation table is written by hand beside a sentinel list
// that is also written by hand. Enumerating the service's own list keeps the
// two in step: a sentinel added there must gain a mapping here, or it reaches
// a companion as an opaque server error instead of the contract code the
// operation documents.
func TestEveryServiceSentinelIsTranslated(t *testing.T) {
	contractErrors := map[error]bool{
		ErrInvalidCredentials: true,
		ErrConflict:           true,
		ErrInvalid:            true,
	}
	for _, sentinel := range compatapp.Sentinels() {
		mapped := mapCompatAppError(sentinel)
		matched := false
		for contractError := range contractErrors {
			if errors.Is(mapped, contractError) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("service sentinel %q maps to %v, which is not one of the contract's translated errors — it would surface as an opaque server error", sentinel, mapped)
		}
	}
}
