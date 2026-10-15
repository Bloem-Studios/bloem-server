package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
)

// NativeStorageS3Browser lists buckets and folders for the Add-source form
// with caller-supplied credentials. nativestorage.S3Browser is the production
// implementation; it owns the endpoint guard, rate limit and concurrency cap.
type NativeStorageS3Browser interface {
	ListBuckets(ctx context.Context, opts nativestorage.S3BrowseOptions, conn nativestorage.S3BrowseConnection) ([]nativestorage.S3Bucket, error)
	ListFolders(ctx context.Context, opts nativestorage.S3BrowseOptions, conn nativestorage.S3BrowseConnection, bucket, prefix, cursor string) (nativestorage.S3FolderPage, error)
}

// nativeStorageS3BucketsRequest is the complete bucket listing body. The
// credentials exist only for this request; they are never stored or logged.
type nativeStorageS3BucketsRequest struct {
	Endpoint     string `json:"endpoint"`
	Region       string `json:"region"`
	AccessKey    string `json:"access_key"`
	SecretKey    string `json:"secret_key"`
	SessionToken string `json:"session_token"`
}

// nativeStorageS3FoldersRequest adds the bucket, prefix and continuation cursor.
type nativeStorageS3FoldersRequest struct {
	nativeStorageS3BucketsRequest
	Bucket string `json:"bucket"`
	Prefix string `json:"prefix"`
	Cursor string `json:"cursor"`
}

type nativeStorageS3BucketsResponse struct {
	Buckets []nativestorage.S3Bucket `json:"buckets"`
}

func (req *nativeStorageS3BucketsRequest) normalize() error {
	for _, field := range []*string{&req.Endpoint, &req.Region, &req.AccessKey, &req.SecretKey, &req.SessionToken} {
		*field = strings.TrimSpace(*field)
	}
	return nativestorage.ValidateS3BrowseCredentials(req.connection())
}

func (req nativeStorageS3BucketsRequest) connection() nativestorage.S3BrowseConnection {
	return nativestorage.S3BrowseConnection{Endpoint: req.Endpoint, Region: req.Region, AccessKey: req.AccessKey, SecretKey: req.SecretKey, SessionToken: req.SessionToken}
}

func (req *nativeStorageS3FoldersRequest) normalize() error {
	if err := req.nativeStorageS3BucketsRequest.normalize(); err != nil {
		return err
	}
	// The prefix and cursor are opaque S3 values and are not trimmed.
	req.Bucket = strings.TrimSpace(req.Bucket)
	if !nativestorage.ValidS3BrowseBucket(req.Bucket) || !nativestorage.ValidS3BrowsePrefix(req.Prefix) || !nativestorage.ValidS3BrowseCursor(req.Cursor) {
		return nativeStorageInvalid()
	}
	return nil
}

// nativeStorageS3Audit records every browse request once, without secrets or
// bodies: who asked, in which scope, which host and bucket, and the outcome.
type nativeStorageS3Audit struct {
	operation string
	actor     auth.AdminContextClaims
	endpoint  string
	bucket    string
}

func (a nativeStorageS3Audit) log(ctx context.Context, err error) {
	outcome := "ok"
	if err != nil {
		outcome = NativeStorageAPIError(err).Code
	}
	attrs := []any{
		"component", "native_storage",
		"operation", a.operation,
		"account_id", a.actor.AccountID,
		"scope", string(a.actor.Scope),
		"endpoint_host", nativestorage.S3BrowseEndpointHost(a.endpoint),
		"outcome", outcome,
	}
	if a.actor.Scope == auth.AdminScopeOrganization {
		attrs = append(attrs, "organization_id", a.actor.OrganizationID.String())
	}
	if nativestorage.ValidS3BrowseBucket(a.bucket) {
		attrs = append(attrs, "bucket", a.bucket)
	}
	if reason := nativestorage.S3BrowseReason(err); reason != "" {
		attrs = append(attrs, "reason", reason)
	}
	slog.InfoContext(ctx, "native storage s3 browse", attrs...)
}

func nativeStorageS3Options(actor auth.AdminContextClaims) nativestorage.S3BrowseOptions {
	return nativestorage.S3BrowseOptions{
		Actor: "account:" + strconv.Itoa(actor.AccountID),
		// Organization administrators may only reach well-known S3 ports.
		RestrictPorts: actor.Scope != auth.AdminScopePlatform,
	}
}

// s3BrowseDecode reads the strict body and returns the error instead of
// writing it, so the audit line records every outcome.
func (h *BloemNativeStorageManagementHandler) s3BrowseDecode(w http.ResponseWriter, r *http.Request, target interface{ normalize() error }) error {
	if _, err := nativeStorageQuery(r); err != nil {
		return err
	}
	if _, err := nativeStorageJSON(w, r, target); err != nil {
		return err
	}
	if err := target.normalize(); err != nil {
		return err
	}
	if h == nil || h.S3Browse == nil {
		return &catalog.NativeOnboardingError{Code: nativeStorageUnavailableCode}
	}
	return nil
}

func nativeStorageS3Error(w http.ResponseWriter, err error) {
	if errors.Is(err, nativestorage.ErrS3BrowseBusy) {
		w.Header().Set("Retry-After", "2")
	}
	writeNativeStorageError(w, err, false)
}

// HandleS3Buckets lists the buckets the supplied credentials can see. It is
// stateless and available to platform and organization administrators.
func (h *BloemNativeStorageManagementHandler) HandleS3Buckets(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var req nativeStorageS3BucketsRequest
	err := h.s3BrowseDecode(w, r, &req)
	audit := nativeStorageS3Audit{operation: "list_buckets", actor: actor, endpoint: req.Endpoint}
	var buckets []nativestorage.S3Bucket
	if err == nil {
		buckets, err = h.S3Browse.ListBuckets(r.Context(), nativeStorageS3Options(actor), req.connection())
	}
	audit.log(r.Context(), err)
	if err != nil {
		nativeStorageS3Error(w, err)
		return
	}
	if buckets == nil {
		buckets = []nativestorage.S3Bucket{}
	}
	nativeStorageWrite(w, http.StatusOK, nativeStorageS3BucketsResponse{Buckets: buckets})
}

// HandleS3Folders lists one page of the immediate child folders of a prefix.
func (h *BloemNativeStorageManagementHandler) HandleS3Folders(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var req nativeStorageS3FoldersRequest
	err := h.s3BrowseDecode(w, r, &req)
	audit := nativeStorageS3Audit{operation: "list_folders", actor: actor, endpoint: req.Endpoint, bucket: req.Bucket}
	var page nativestorage.S3FolderPage
	if err == nil {
		page, err = h.S3Browse.ListFolders(r.Context(), nativeStorageS3Options(actor), req.connection(), req.Bucket, req.Prefix, req.Cursor)
	}
	audit.log(r.Context(), err)
	if err != nil {
		nativeStorageS3Error(w, err)
		return
	}
	if page.Folders == nil {
		page.Folders = []nativestorage.S3Folder{}
	}
	nativeStorageWrite(w, http.StatusOK, page)
}
