package silo.permission

import rego.v1

bloem_live_tv_input := object.union(base_input, {"permission": "watch_live_tv"})

test_bloem_live_tv_assigned_permission_allows if {
	got := decision with input as object.union(bloem_live_tv_input, {"assigned_permissions": ["watch_live_tv"]})
	got.allowed
}

test_bloem_live_tv_acting_admin_allows if {
	got := decision with input as object.union(bloem_live_tv_input, {"role": "admin"})
	got.allowed
}

test_bloem_live_tv_without_permission_denies if {
	got := decision with input as bloem_live_tv_input
	not got.allowed
	got.reason_code == "live_tv_forbidden"
}

test_bloem_live_tv_disabled_account_denies if {
	got := decision with input as object.union(bloem_live_tv_input, {
		"user_enabled": false,
		"assigned_permissions": ["watch_live_tv"],
	})
	not got.allowed
}
