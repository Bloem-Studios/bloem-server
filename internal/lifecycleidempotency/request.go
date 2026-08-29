package lifecycleidempotency

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"net/url"
	"sort"
	"strings"
)

type RequestDigester func(method, routeID string, selectors map[string]string, query url.Values, body []byte) Digest
type PreauthActorDigester func(intent string, trustAnchors ...string) Digest

func NewRequestDigester(secret []byte) RequestDigester {
	key := append([]byte(nil), secret...)
	return func(method, routeID string, selectors map[string]string, query url.Values, body []byte) Digest {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte("bloem.lifecycle-request.v1\x00"))
		writeRequestDigestPart(mac, []byte(strings.ToUpper(strings.TrimSpace(method))))
		writeRequestDigestPart(mac, []byte(routeID))
		selectorKeys := make([]string, 0, len(selectors))
		for name := range selectors {
			selectorKeys = append(selectorKeys, name)
		}
		sort.Strings(selectorKeys)
		for _, name := range selectorKeys {
			writeRequestDigestPart(mac, []byte(name))
			writeRequestDigestPart(mac, []byte(selectors[name]))
		}
		writeRequestDigestPart(mac, []byte(canonicalQuery(query)))
		writeRequestDigestPart(mac, body)
		var digest Digest
		copy(digest[:], mac.Sum(nil))
		return digest
	}
}

func NewPreauthActorDigester(secret []byte) PreauthActorDigester {
	key := append([]byte(nil), secret...)
	return func(intent string, trustAnchors ...string) Digest {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte("bloem.lifecycle-preauth-actor.v1\x00"))
		writeRequestDigestPart(mac, []byte(intent))
		for _, anchor := range trustAnchors {
			writeRequestDigestPart(mac, []byte(anchor))
		}
		var digest Digest
		copy(digest[:], mac.Sum(nil))
		return digest
	}
}

func canonicalQuery(query url.Values) string {
	if len(query) == 0 {
		return ""
	}
	canonical := make(url.Values, len(query))
	for key, values := range query {
		canonical[key] = append([]string(nil), values...)
		sort.Strings(canonical[key])
	}
	return canonical.Encode()
}

func writeRequestDigestPart(writer digestWriter, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write(value)
}
