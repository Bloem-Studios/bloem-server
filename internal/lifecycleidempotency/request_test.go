package lifecycleidempotency

import (
	"net/url"
	"testing"
)

func TestRequestDigesterCanonicalizesSelectorsAndQueryButBindsRouteAndBody(t *testing.T) {
	digest := NewRequestDigester([]byte("request-digest-test-secret"))
	first := digest("put", "account.update", map[string]string{"profile": "p1", "account": "42"}, url.Values{"z": {"2", "1"}, "a": {"x"}}, []byte(`{"enabled":false}`))
	second := digest("PUT", "account.update", map[string]string{"account": "42", "profile": "p1"}, url.Values{"a": {"x"}, "z": {"2", "1"}}, []byte(`{"enabled":false}`))
	if first != second {
		t.Fatalf("canonical request digests differ: %x != %x", first, second)
	}
	for name, changed := range map[string]Digest{
		"route":    digest("PUT", "account.delete", map[string]string{"account": "42", "profile": "p1"}, url.Values{"a": {"x"}, "z": {"2", "1"}}, []byte(`{"enabled":false}`)),
		"selector": digest("PUT", "account.update", map[string]string{"account": "43", "profile": "p1"}, url.Values{"a": {"x"}, "z": {"2", "1"}}, []byte(`{"enabled":false}`)),
		"body":     digest("PUT", "account.update", map[string]string{"account": "42", "profile": "p1"}, url.Values{"a": {"x"}, "z": {"2", "1"}}, []byte(`{"enabled":true}`)),
	} {
		if changed == first {
			t.Errorf("changing %s did not change request digest", name)
		}
	}
}

func TestPreauthActorDigesterSeparatesIntentAndTrustAnchors(t *testing.T) {
	digest := NewPreauthActorDigester([]byte("preauth-actor-test-secret"))
	first := digest("signup", "server-instance", "invite-code-digest")
	if first != digest("signup", "server-instance", "invite-code-digest") {
		t.Fatal("identical preauth subject produced a different digest")
	}
	for name, changed := range map[string]Digest{
		"intent": digest("invitation.accept", "server-instance", "invite-code-digest"),
		"server": digest("signup", "replacement-server", "invite-code-digest"),
		"anchor": digest("signup", "server-instance", "other-code-digest"),
	} {
		if changed == first {
			t.Errorf("changing %s did not change preauth actor digest", name)
		}
	}
}
