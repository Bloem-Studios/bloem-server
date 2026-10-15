package handlers

import (
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

// validateNativeStorageCredentials checks an already-decoded body. Unknown and
// duplicate fields are rejected earlier, by h.command, before this runs.
func validateNativeStorageCredentials(cmd nativeStorageCredentialsCommand) error {
	if cmd.ExpectedRevision <= 0 || cmd.AccessKey == "" || cmd.SecretKey == "" {
		return nativeStorageInvalid()
	}
	for _, v := range []string{cmd.AccessKey, cmd.SecretKey, cmd.SessionToken} {
		for _, r := range v {
			if unicode.IsControl(r) {
				return nativeStorageInvalid()
			}
		}
	}
	return nil
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
	var cmd nativeStorageCredentialsCommand
	if _, ok = h.command(w, r, &cmd); !ok {
		return
	}
	if err := validateNativeStorageCredentials(cmd); err != nil {
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
