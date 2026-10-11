package mediasource

import (
	"fmt"
	"math"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

// Audio metadata describes a book and track; file identity and revision remain
// independently required by the existing storage entry/ref validators.
func validateAudioMetadata(entry *storagev1.Entry) error {
	if book := entry.GetAudiobook(); book != nil {
		for _, value := range []string{book.GetBookId(), book.GetTitle(), book.GetPublisher(), book.GetPublishedDate(), book.GetLanguage(), book.GetIsbn(), book.GetSeries(), book.GetSeriesIndex(), book.GetCoverEntryId(), book.GetCoverRevision(), book.GetCoverThumbhash()} {
			if err := validateText(value, 4096, false); err != nil {
				return err
			}
		}
		if err := validateText(book.GetDescription(), 64<<10, false); err != nil {
			return err
		}
		for _, values := range [][]string{book.GetAuthors(), book.GetNarrators(), book.GetGenres()} {
			if len(values) > 64 {
				return fmt.Errorf("invalid audio metadata list size")
			}
			for _, value := range values {
				if err := validateText(value, 4096, false); err != nil {
					return err
				}
			}
		}
	}
	audio := entry.GetAudioFile()
	if audio == nil {
		return nil
	}
	for _, value := range []string{audio.GetBookId(), audio.GetCodec(), audio.GetContainer()} {
		if err := validateText(value, 4096, false); err != nil {
			return err
		}
	}
	if !validAudioTime(audio.GetDurationSeconds()) {
		return fmt.Errorf("invalid audio duration")
	}
	if len(audio.GetChapters()) > 1024 {
		return fmt.Errorf("invalid audio chapter count")
	}
	seen := make(map[string]struct{}, len(audio.GetChapters()))
	for _, chapter := range audio.GetChapters() {
		if chapter == nil {
			return fmt.Errorf("nil audio chapter")
		}
		if err := validateText(chapter.GetId(), 4096, true); err != nil {
			return err
		}
		if err := validateText(chapter.GetTitle(), 4096, false); err != nil {
			return err
		}
		if _, duplicate := seen[chapter.GetId()]; duplicate {
			return fmt.Errorf("duplicate audio chapter identity")
		}
		seen[chapter.GetId()] = struct{}{}
		start, end := chapter.GetStartSeconds(), chapter.GetEndSeconds()
		if !validAudioTime(start) || !validAudioTime(end) || end < start || end > audio.GetDurationSeconds() {
			return fmt.Errorf("invalid audio chapter timing")
		}
	}
	return nil
}

func validAudioTime(seconds float64) bool {
	return !math.IsNaN(seconds) && !math.IsInf(seconds, 0) && seconds >= 0
}
