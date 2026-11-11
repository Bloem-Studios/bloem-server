package catalog

// Placeholder for Task 7 of the search mirror plan, so parallel tasks compile.
// The Task 7 implementer deletes this file and writes the real code.

import "context"

type searchMirrorTargets struct {
	Main, Mirror       string
	MainErr, MirrorErr error
}

type searchMirrorVerdict string

const (
	verdictNormal     searchMirrorVerdict = "normal"
	verdictSwapped    searchMirrorVerdict = "swapped"
	verdictClone      searchMirrorVerdict = "clone"
	verdictNewMirror  searchMirrorVerdict = "new_mirror"
	verdictNewMain    searchMirrorVerdict = "new_main"
	verdictMirrorDown searchMirrorVerdict = "mirror_down"
	verdictFresh      searchMirrorVerdict = "fresh"
)

func judgeSearchMirrorTargets(rememberedMain, rememberedMirror string, seen searchMirrorTargets) searchMirrorVerdict {
	panic("search mirror: task 7 placeholder")
}

func (m *SearchMirror) settleTargets(ctx context.Context, row *searchMirrorRow) (searchMirrorVerdict, error) {
	panic("search mirror: task 7 placeholder")
}
