package handlers

import (
	"context"
	"log/slog"
	"time"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

// updateProfileWithLifecycle commits profile columns and canonical setting
// projections together, then applies the same best-effort object cleanup for
// every caller of the profile mutation domain.
func (h *ProfileHandler) updateProfileWithLifecycle(
	ctx context.Context,
	store userstore.UserStore,
	userID int,
	current *userstore.Profile,
	input userstore.UpdateProfileInput,
	settingsSync []profileSettingSync,
) error {
	if err := h.applyProfileUpdateSettingsSync(
		ctx, store, userID, current.ID, input, settingsSync,
	); err != nil {
		return err
	}
	h.completeProfileUpdateAfterCommit(ctx, userID, current, input)
	return nil
}

func (h *ProfileHandler) completeProfileUpdateAfterCommit(ctx context.Context, userID int, current *userstore.Profile, input userstore.UpdateProfileInput) {
	if current.Avatar != "" && input.Avatar != nil && avatarRefReplacesUpload(current.Avatar, *input.Avatar) {
		if cleanupErr := deleteUploadedAvatarObjects(ctx, h.AvatarStore, userID, current.ID); cleanupErr != nil {
			slog.WarnContext(ctx, "profile avatar cleanup failed after update",
				"component", "api", "user_id", userID, "profile_id", current.ID, "error", cleanupErr)
		}
	}
}

// deleteProfileWithLifecycle removes the profile first, matching the native
// endpoint's transaction boundary, then best-effort cleans uploaded avatar
// objects and shared device/download rows. Cleanup failure never resurrects a
// deleted profile or changes the successful HTTP result.
func (h *ProfileHandler) deleteProfileWithLifecycle(
	ctx context.Context,
	store userstore.UserStore,
	userID int,
	profile *userstore.Profile,
) error {
	if err := store.DeleteProfile(ctx, profile.ID); err != nil {
		return err
	}
	h.completeProfileDeleteAfterCommit(ctx, userID, profile)
	return nil
}

func (h *ProfileHandler) completeProfileDeleteAfterCommit(ctx context.Context, userID int, profile *userstore.Profile) {
	if isUploadedAvatarRef(profile.Avatar) {
		if cleanupErr := deleteUploadedAvatarObjects(ctx, h.AvatarStore, userID, profile.ID); cleanupErr != nil {
			slog.WarnContext(ctx, "profile avatar cleanup failed after delete",
				"component", "api", "user_id", userID, "profile_id", profile.ID, "error", cleanupErr)
		}
	}
	if h.DeviceLibraryPurger != nil {
		purgeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if purgeErr := h.DeviceLibraryPurger.PurgeProfileDevices(purgeCtx, userID, profile.ID); purgeErr != nil {
			slog.WarnContext(ctx, "profile device-library purge failed after delete",
				"component", "api", "user_id", userID, "profile_id", profile.ID, "error", purgeErr)
		}
	}
}
