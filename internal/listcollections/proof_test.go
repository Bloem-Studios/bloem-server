package listcollections

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestListSnapshotSignedProofIsExactAndExpiring(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	want := SourceProof{Version: 1, InstanceID: "instance", AccountID: "42", ProfileID: "profile", SourceID: "personal:source", SnapshotID: "snapshot", Digest: Hash{1}, IssuedAt: snapshotNow, ExpiresAt: snapshotNow.Add(time.Minute)}
	sign := func(p SourceProof) string {
		raw, e := json.Marshal(p)
		if e != nil {
			t.Fatal(e)
		}
		return "lp1." + base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, append([]byte("bloem.list.proof.v1\x00"), raw...)))
	}
	public := base64.RawURLEncoding.EncodeToString(pub)
	if err = VerifySourceProof(public, sign(want), want, snapshotNow); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"version", "instance", "account", "profile", "source", "snapshot", "digest", "issued", "expiry"} {
		t.Run(field, func(t *testing.T) {
			bad := want
			switch field {
			case "version":
				bad.Version = 2
			case "instance":
				bad.InstanceID = "other"
			case "account":
				bad.AccountID = "other"
			case "profile":
				bad.ProfileID = "other"
			case "source":
				bad.SourceID = "other"
			case "snapshot":
				bad.SnapshotID = "other"
			case "digest":
				bad.Digest[0]++
			case "issued":
				bad.IssuedAt = snapshotNow.Add(time.Second)
			case "expiry":
				bad.ExpiresAt = bad.IssuedAt.Add(121 * time.Second)
			}
			if VerifySourceProof(public, sign(bad), want, snapshotNow) == nil {
				t.Fatal("foreign or invalid signed proof accepted")
			}
		})
	}
	if VerifySourceProof(public, sign(want), want, want.ExpiresAt) == nil {
		t.Fatal("expired proof accepted")
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if VerifySourceProof(base64.RawURLEncoding.EncodeToString(other), sign(want), want, snapshotNow) == nil {
		t.Fatal("substituted signing key accepted")
	}
	if VerifySourceProof(public, sign(want)+"x", want, snapshotNow) == nil {
		t.Fatal("modified signature accepted")
	}
}
