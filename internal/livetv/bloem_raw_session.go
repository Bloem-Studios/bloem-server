package livetv

import "context"

// StartRawChannelSession claims a tuner for a caller that proxies the tuner's
// MPEG-TS itself (the Jellyfin-protocol surface). It takes the same capacity,
// reclaim and conflict path as StartChannelSession but never starts the HLS
// bridge: a remux nobody reads opens a second tuner connection the claimed
// index does not account for, runs an ffmpeg process, and may hold an encode
// slot, all for nothing.
//
// The session's StreamURL is the raw tuner URL; never hand it to a client.
func (s *Service) StartRawChannelSession(ctx context.Context, channelID string, userID int, profileID string) (*LiveSession, error) {
	return s.startChannelSession(ctx, channelID, userID, profileID, ClientCapabilities{}, false)
}
