package procgroup

import (
	"errors"
	"os/exec"
	"path"
	"strings"
)

// ErrNativeInput is the error a command from CommandContext returns, without
// starting, when its input is a native catalog location.
var ErrNativeInput = errors.New("media tool input is a native catalog location")

// NativeLocation reports whether a catalog location's bytes come only from a
// Bloem reader: a storage identity, or a local `.strm` file, whose text is a
// remote URL anyone who can write to the library folder controls. It is
// storagesource.IsNativeLocation, kept here so this package needs no storage
// imports; a test pins the two together.
func NativeLocation(location string) bool {
	return strings.HasPrefix(location, "bloem-storage:") || strings.EqualFold(path.Ext(location), ".strm")
}

// refuseNativeInput is CommandContext's hook: ffmpeg and ffprobe never open a
// native catalog location, an argument after -i or ffprobe's trailing input.
// ffmpeg would probe a `.strm` file's text as media (a playlist or concat
// list among the formats it guesses). Bloem's readers hand the tools a
// private address for the opened source instead. The command then fails at
// Start with ErrNativeInput.
func refuseNativeInput(cmd *exec.Cmd, args []string) {
	if cmd.Err != nil {
		return
	}
	for i, arg := range args {
		if (i == len(args)-1 || (i > 0 && args[i-1] == "-i")) && nativeToolInput(arg) {
			cmd.Err = ErrNativeInput
			return
		}
	}
}

func nativeToolInput(arg string) bool {
	if len(arg) >= len("file:") && strings.EqualFold(arg[:len("file:")], "file:") {
		arg = arg[len("file:"):]
	}
	return NativeLocation(arg)
}
