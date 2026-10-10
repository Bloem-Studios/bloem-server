//go:build unix

package plugins

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Walk from / using descriptor-relative, no-follow operations. A writable or
// untrusted ancestor could replace an otherwise private subtree; sticky
// directories are refused too. The registry root and all state directories
// must belong to this host UID and have exactly 0700 permissions.
func nativeS3PrivateDirectory(path, baseDir string) (int, error) {
	if !filepath.IsAbs(baseDir) || filepath.Clean(baseDir) != baseDir || baseDir == string(filepath.Separator) {
		return -1, errors.New("invalid S3 state root")
	}
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, errors.New("cannot open S3 state ancestor")
	}
	ok := false
	defer func() {
		if !ok {
			_ = unix.Close(fd)
		}
	}()
	current := string(filepath.Separator)
	uid := uint32(os.Geteuid())
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil || !nativeS3DirectoryTrusted(&stat, uid, false) {
		return -1, errors.New("unsafe S3 state ancestor")
	}
	for _, segment := range strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator)) {
		if segment == "" || segment == "." || segment == ".." {
			return -1, errors.New("invalid S3 state directory")
		}
		current = filepath.Join(current, segment)
		next, openErr := unix.Openat(fd, segment, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(openErr, unix.ENOENT) {
			if err = unix.Mkdirat(fd, segment, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
				return -1, errors.New("cannot create private S3 state directory")
			}
			next, openErr = unix.Openat(fd, segment, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		if openErr != nil {
			return -1, errors.New("cannot open private S3 state directory")
		}
		_ = unix.Close(fd)
		fd = next
		private := current == baseDir || strings.HasPrefix(current, baseDir+string(filepath.Separator))
		if err = unix.Fstat(fd, &stat); err != nil || !nativeS3DirectoryTrusted(&stat, uid, private) {
			return -1, errors.New("unsafe S3 state directory")
		}
	}
	ok = true
	return fd, nil
}

func nativeS3DirectoryTrusted(stat *unix.Stat_t, uid uint32, private bool) bool {
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0022 != 0 || (stat.Uid != uid && stat.Uid != 0) {
		return false
	}
	return !private || (stat.Uid == uid && stat.Mode&07777 == 0700)
}

func nativeS3PrepareRoot(baseDir string) error {
	fd, err := nativeS3PrivateDirectory(filepath.Join(baseDir, "state", "s3"), baseDir)
	if err == nil {
		_ = unix.Close(fd)
	}
	return err
}

func nativeS3StatePath(baseDir string, installationID int) (string, error) {
	if installationID <= 0 {
		return "", errors.New("invalid S3 installation identity")
	}
	dir := filepath.Join(baseDir, "state", "s3", "installation-"+strconv.Itoa(installationID))
	fd, err := nativeS3PrivateDirectory(dir, baseDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = unix.Close(fd) }()
	var stat unix.Stat_t
	err = unix.Fstatat(fd, "index.db", &stat, unix.AT_SYMLINK_NOFOLLOW)
	if !errors.Is(err, unix.ENOENT) {
		if err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&07777 != 0600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
			return "", errors.New("unsafe S3 state file")
		}
	}
	return filepath.Join(dir, "index.db"), nil
}
