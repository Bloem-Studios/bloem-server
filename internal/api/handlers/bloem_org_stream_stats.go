package handlers

// Bloem-owned. The organization's viewing stats count as Silo's playback
// Insights does (admin_insights_plays.go and the Devices tab), so an
// organization administrator and a platform administrator read the same numbers
// for the same people. Silo's Insights covers the whole server and is for
// platform administrators only; these are its definitions, scoped to one
// organization's members by bloeminsights.

import (
	"fmt"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/bloeminsights"
)

// bloemSiloHistory hands bloeminsights Silo's Insights definitions.
func bloemSiloHistory() bloeminsights.SiloHistory {
	return bloeminsights.SiloHistory{
		Viewings:  bloemViewingRowsSQL,
		Direct:    insightsDirectSQL,
		Transcode: insightsTranscodeSQL,
	}
}

// bloemViewingRowsSQL is insightsPlaysScope.viewingRowsSQL for a window
// [from, to) with scopeCond as the account filter: Silo's viewing grouping
// (insightsViewingCTE), the look-back it needs, its in-window predicate and its
// play predicate. Silo's scope filters one account at a time; an organization
// is a set of accounts, so the scope condition comes from the caller.
func bloemViewingRowsSQL(scopeCond string, from, to time.Time, args *[]any) (with, counted, isPlay string) {
	param := func(v any) string {
		*args = append(*args, v)
		return fmt.Sprintf("$%d", len(*args))
	}
	fromParam := param(from)
	conds := []string{
		"h.started_at < " + param(to),
		fmt.Sprintf("h.ended_at >= %s::timestamptz - interval '%s'", fromParam, insightsViewingGap),
		scopeCond,
	}
	with = fmt.Sprintf(insightsViewingCTE,
		adminPlaybackWatchTimeSQL("h"), "h.started_at >= "+fromParam, strings.Join(conds, " AND "),
		insightsReplacedHandoffGrace, insightsViewingGap,
		insightsDirectSQL("q.play_method"), insightsRemuxSQL("q.play_method"), insightsTranscodeSQL("q.play_method"),
		"", "") // no file columns or media_files join: an organization's stats have no library filter
	return with, "vr.in_window", insightsIsPlaySQL("vr")
}
