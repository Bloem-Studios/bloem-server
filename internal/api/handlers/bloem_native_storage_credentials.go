package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"unicode"

	"github.com/Silo-Server/silo-server/internal/plugins"
)

// nativeStorageCredentialsCommand is the complete credential rotation body.
// It cannot name any other connection field, so a rotation cannot change the
// bucket, prefix, endpoint or any other setting.
type nativeStorageCredentialsCommand struct {
	ExpectedRevision int64  `json:"expected_revision"`
	AccessKey        string `json:"access_key"`
	SecretKey        string `json:"secret_key"`
	SessionToken     string `json:"session_token"`
}

func nativeStorageCredentialsDecode(data []byte) (nativeStorageCredentialsCommand, error) {
	var cmd nativeStorageCredentialsCommand
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cmd); err != nil || dec.More() {
		return cmd, nativeStorageInvalid()
	}
	if cmd.ExpectedRevision <= 0 || cmd.AccessKey == "" || cmd.SecretKey == "" {
		return cmd, nativeStorageInvalid()
	}
	for _, v := range []string{cmd.AccessKey, cmd.SecretKey, cmd.SessionToken} {
		for _, r := range v {
			if unicode.IsControl(r) {
				return cmd, nativeStorageInvalid()
			}
		}
	}
	return cmd, nil
}

// HandleRotateCredentials replaces only the stored S3 credentials of a source,
// including one in use by a library. The response carries the new revision and
// never echoes any credential.
func (h *BloemNativeStorageManagementHandler) HandleRotateCredentials(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	key, ok := nativeStoragePathUUID(w, r, "source_key")
	if !ok {
		return
	}
	var raw nativeStorageCredentialsCommand
	if _, ok = h.command(w, r, &raw); !ok {
		return
	}
	cmd, err := nativeStorageCredentialsDecode(mustJSON(raw))
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	if !h.sourceAvailable(w) {
		return
	}
	revision, err := h.Sources.RotateCredentials(r.Context(), actor, key, cmd.ExpectedRevision, plugins.S3Credentials{AccessKey: cmd.AccessKey, SecretKey: cmd.SecretKey, SessionToken: cmd.SessionToken})
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	nativeStorageWrite(w, 200, nativeStorageConfigurationResponse{SourceKey: key, Revision: revision})
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
