package mediasample

import "github.com/Silo-Server/silo-server/internal/procgroup"

// nativeInputLog is the log tail of a refused native input. It names
// ffmpeg's invalid-data failure, the one ffmpeg reported for a `.strm` file's
// text before the refusal, so Classify still reads it as permanent and a job
// does not retry the file.
const nativeInputLog = "native catalog location: invalid data found when processing input"

// refuseNativeInput is Run's hook: a native catalog location (a `.strm` file,
// a storage identity) never reaches ffmpeg, neither as -i nor inside the
// concat list a sampling run writes to stdin, where procgroup's argument check
// cannot see it.
func refuseNativeInput(input string) error {
	if !procgroup.NativeLocation(input) {
		return nil
	}
	return &Error{Reason: ReasonExit, Attempts: []AttemptError{{
		Decoder: softwareDecoder, Reason: ReasonExit, Err: procgroup.ErrNativeInput, StderrTail: nativeInputLog,
	}}}
}
