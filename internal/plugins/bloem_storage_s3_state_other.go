//go:build !unix

package plugins

import "errors"

func nativeS3PrepareRoot(string) error {
	return errors.New("private S3 state requires Unix filesystem ownership checks")
}

func nativeS3StatePath(string, int) (string, error) {
	return "", nativeS3PrepareRoot("")
}
