package silo.permission

import rego.v1

# Bloem: Live TV is allowed to an acting administrator, or to an enabled
# account assigned watch_live_tv. permission.rego's base_decision routes the
# watch_live_tv permission here.
bloem_live_tv_decision(i) := allow if {
	acting_admin_allowed(i)
} else := allow if {
	user_enabled(i)
	assigned_permission(i, "watch_live_tv")
} else := deny("Live TV permission required", "live_tv_forbidden")
