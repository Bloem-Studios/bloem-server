//go:build unix

package plugins

import (
	"testing"

	"golang.org/x/sys/unix"
)

// Root-owned ancestors are trusted, but another account's directory and a
// root-owned private leaf cannot substitute for this host's private state.
func TestNativeS3DirectoryOwnerPolicy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stat    unix.Stat_t
		private bool
		want    bool
	}{
		{"host-ancestor", unix.Stat_t{Uid: 1000, Mode: unix.S_IFDIR | 0755}, false, true},
		{"root-ancestor", unix.Stat_t{Uid: 0, Mode: unix.S_IFDIR | 0755}, false, true},
		{"foreign-ancestor", unix.Stat_t{Uid: 1001, Mode: unix.S_IFDIR | 0755}, false, false},
		{"host-private", unix.Stat_t{Uid: 1000, Mode: unix.S_IFDIR | 0700}, true, true},
		{"root-private", unix.Stat_t{Uid: 0, Mode: unix.S_IFDIR | 0700}, true, false},
		{"readable-private", unix.Stat_t{Uid: 1000, Mode: unix.S_IFDIR | 0750}, true, false},
		{"writable-ancestor", unix.Stat_t{Uid: 1000, Mode: unix.S_IFDIR | 0770}, false, false},
		{"sticky-ancestor", unix.Stat_t{Uid: 0, Mode: unix.S_IFDIR | 01777}, false, false},
		{"regular-file", unix.Stat_t{Uid: 1000, Mode: unix.S_IFREG | 0700}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if nativeS3DirectoryTrusted(&tc.stat, 1000, tc.private) != tc.want {
				t.Fatal("wrong host directory ownership/permission admission")
			}
		})
	}
}
