package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ExtractSourceSubtitleFonts uses separate authorized opens for metadata and
// bytes. The immutable source binding prevents attachments changing between
// them; probed lengths and the output budget bound the complete extract.
func ExtractSourceSubtitleFonts(ctx context.Context, opts TranscodeOpts) ([]SubtitleFontAttachment, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	select {
	case subtitleFontProbeSlots <- struct{}{}:
		defer func() { <-subtitleFontProbeSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	data, err := sourceVideoTool(ctx, opts, ffprobePathFromFFmpeg(ResolveFFmpegPath(opts.FFmpegPath)), 1<<20,
		"-v", "error", "-select_streams", "t", "-show_entries", "stream=index,codec_name,codec_type,extradata_size:stream_tags=filename,mimetype", "-of", "json", opts.InputPath)
	if err != nil {
		return nil, err
	}
	var probed attachmentProbeOutput
	if err := json.Unmarshal(data, &probed); err != nil {
		return nil, err
	}
	var streams []attachmentProbeStream
	var expected int64
	args := []string{"-v", "error", "-nostdin", "-y"}
	for _, stream := range probed.Streams {
		if !isFontAttachment(stream) {
			continue
		}
		if len(streams) == maxSubtitleFontAttachments {
			break
		}
		if stream.Index < 0 || stream.ExtraDataSize <= 0 || stream.ExtraDataSize > maxSubtitleFontBytes-expected {
			return nil, errors.New("source font data exceeds budget")
		}
		expected += stream.ExtraDataSize
		streams = append(streams, stream)
		args = append(args, fmt.Sprintf("-dump_attachment:%d", stream.Index), "pipe:1")
	}
	if expected == 0 {
		return nil, nil
	}
	// Match Silo's extractor: attachments are dumped from the first input while
	// a synthetic zero-duration output avoids waiting for real media packets.
	args = append(args, "-i", opts.InputPath,
		"-f", "lavfi", "-i", "anullsrc=r=8000:cl=mono",
		"-map", "1:a:0", "-c", "copy", "-t", "0", "-f", "null", "-")
	data, err = sourceVideoTool(ctx, opts, ResolveFFmpegPath(opts.FFmpegPath), int(expected), args...)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != expected {
		return nil, errors.New("source font data differs from probed size")
	}
	fonts := make([]SubtitleFontAttachment, 0, len(streams))
	offset := 0
	for i, stream := range streams {
		end := offset + int(stream.ExtraDataSize)
		fonts = append(fonts, SubtitleFontAttachment{StreamIndex: stream.Index, Name: safeAttachmentDisplayName(stream, fmt.Sprintf("attachment-%d%s", i, fontAttachmentExt(stream))), Data: data[offset:end:end]})
		offset = end
	}
	return fonts, nil
}
