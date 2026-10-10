package downloads

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
)

// NewProfilePrepareAgainService binds Bloem's profile authority before calling
// Silo's unchanged retry implementation. Bloem HTTP composition must install
// this adapter; *Service retains Silo's account-only retry contract.
func NewProfilePrepareAgainService(service *Service) *profilePrepareAgainService {
	return &profilePrepareAgainService{service: service}
}

type profilePrepareAgainService struct{ service *Service }

func (a *profilePrepareAgainService) PrepareAgain(ctx context.Context, userID int, profileID, deviceID, downloadID string, filter catalog.AccessFilter) (*Download, error) {
	if profileID == "" || deviceID == "" {
		return nil, ErrProfileRequired
	}
	s := a.service
	ctx = bloemWithDownloadProfile(ctx, userID, profileID)
	cfg, user, err := s.downloadConfigForUser(ctx, userID, deviceID)
	if err != nil {
		return nil, err
	}
	dl, err := s.repo.GetManagedByID(ctx, downloadID, userID, profileID, deviceID)
	if err != nil {
		return nil, err
	}
	// Originals and remuxes do not require transcode permission. The unchanged
	// upstream implementation handles disabled server transcoding or missing
	// artifact support with its existing error and early-return behavior.
	var transcodeErr error
	if cfg.TranscodeEnabled && s.artifacts != nil {
		_, transcodeErr = s.policy.ensureTranscodeAvailable(ctx, user, cfg, true, dl.Quality, deviceID)
		if dl.Format == FormatTranscode && dl.ArtifactID != "" && transcodeErr != nil {
			return nil, transcodeErr
		}
	}
	// Bind the decision to this retry: even a concurrently replaced original
	// cannot make the upstream path prepare a newly forbidden transcode.
	ctx = context.WithValue(ctx, bloemRetryPolicyKey{}, bloemRetryPolicy{
		userID: userID, profileID: profileID, deviceID: deviceID, quality: dl.Quality,
	})
	return s.PrepareAgain(ctx, userID, profileID, deviceID, downloadID, filter)
}

type bloemRetryPolicyKey struct{}

type bloemRetryPolicy struct {
	userID    int
	profileID string
	deviceID  string
	quality   string
}

func (s *Service) bloemRetryDownloadConfig(ctx context.Context, userID int, deviceID string, cfg config.DownloadConfig, user *PolicyUser) config.DownloadConfig {
	retry, ok := ctx.Value(bloemRetryPolicyKey{}).(bloemRetryPolicy)
	if !ok {
		return cfg
	}
	subject, scoped := ctx.Value(bloemDownloadProfileKey{}).(bloemDownloadProfile)
	// A retry scope never grants a capability and may only narrow the server
	// configuration for the same account/profile/device that was authorized.
	matched := scoped &&
		retry.userID == userID && subject.userID == userID &&
		retry.profileID == subject.profileID && retry.deviceID == deviceID
	if !matched || user == nil {
		cfg.TranscodeEnabled = false
		return cfg
	}
	// Re-evaluate against the user/config that the delegated retry just loaded,
	// rather than retaining an earlier grant across a policy change.
	_, err := s.policy.ensureTranscodeAvailable(ctx, user, cfg, s.artifacts != nil, retry.quality, deviceID)
	cfg.TranscodeEnabled = cfg.TranscodeEnabled && err == nil
	return cfg
}
