package api

import (
	"net/http"
	"strings"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"

	"github.com/Silo-Server/silo-server/internal/activitylog"
	"github.com/Silo-Server/silo-server/internal/auth"
)

// guestActivity drops activity rows for guest requests and for the preview
// endpoints themselves: guests are anonymous, and activity_log.client_ip is
// an inet column a blanked address cannot satisfy.
type guestActivity struct {
	activitylog.Writer
	account interface{ CachedAccountID() int }
}

func guestFreeActivity(next activitylog.Writer, settings auth.PreviewGuestSettingsSource) activitylog.Writer {
	account, ok := settings.(interface{ CachedAccountID() int })
	if next == nil || !ok {
		return next
	}
	return &guestActivity{Writer: next, account: account}
}

func (g *guestActivity) Write(entry activitylog.LogEntry) {
	if strings.HasPrefix(entry.Path, "/api/bloem/v1/preview/") {
		return
	}
	if entry.UserID != nil {
		if accountID := g.account.CachedAccountID(); accountID != 0 && *entry.UserID == accountID {
			return
		}
	}
	g.Writer.Write(entry)
}

func bloemPreviewReaderSource(deps Dependencies) auth.PreviewGuestSettingsSource {
	if reader := bloemPreviewReader(deps); reader != nil {
		return reader
	}
	return nil
}

// skipGuestLogging avoids both authenticated and anonymous guest activity.
func skipGuestLogging(logging func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		logged := logging(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if apimw.IsPreviewRequest(r.Context()) {
				next.ServeHTTP(w, r)
				return
			}
			logged.ServeHTTP(w, r)
		})
	}
}
