//go:build !unix

package plugins

import (
	"errors"
	"os"
)

func nativeS3PrepareRoot(string) error {
	return errors.New("private S3 state requires Unix filesystem ownership checks")
}

func nativeS3StatePath(string, int) (string, error) {
	return "", nativeS3PrepareRoot("")
}

func nativeS3ReserveState(string, int) (string, error) {
	return "", nativeS3PrepareRoot("")
}

func nativeS3OpenPrivateFile(string, string, os.FileMode) (*os.File, error) {
	return nil, nativeS3PrepareRoot("")
}
