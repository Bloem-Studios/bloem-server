package handlers

import (
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

// Native request phase failures survive the existing generic adapters. Earlier
// committed authorized phases remain committed; this is not rollback evidence.
func nativePhaseServiceError(err error, status int, code, message string) *APIError {
	var refused *catalog.NativePhaseRefusal
	if !errors.As(err, &refused) {
		return apiError(status, code, message)
	}
	switch refused.Code {
	case "unauthenticated":
		status = http.StatusUnauthorized
		code = "unauthorized"
		message = "Authentication required"
	case "forbidden":
		status = http.StatusForbidden
		code = "forbidden"
		message = "Access forbidden"
	case "not_found":
		status = http.StatusNotFound
		code = "not_found"
		message = "Resource not found"
	case "native_local_operation_unsupported":
		status = http.StatusConflict
		code = refused.Code
		message = "Native local operation is unsupported"
	default:
		status = http.StatusServiceUnavailable
		code = nativeStorageUnavailableCode
		message = "Native storage unavailable"
	}
	return (&APIError{Status: status, Code: code, Message: message}).WithCause(err)
}
