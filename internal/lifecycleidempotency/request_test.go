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
