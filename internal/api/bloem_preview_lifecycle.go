package api

import "net/http"

func init() {
	lifecycleOneShotRoutes = append(lifecycleOneShotRoutes, ReviewedOneShotRoute{
		Method:  http.MethodPut,
		Pattern: NativeAPIPrefix + "/admin/platform/preview/accounts/{account_id}/presentation",
		Reason:  "idempotent preview display metadata; no account lifecycle or entitlement mutation",
	})
}
