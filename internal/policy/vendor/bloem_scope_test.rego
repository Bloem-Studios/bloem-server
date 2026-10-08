package silo.scope

import rego.v1

# Bloem tenancy is opt-in: these cases carry tenant facts (present=true), so
# library_decision takes the tenant-bounded branch. scope_test.rego covers
# Silo's tenant-free path unchanged.
bloem_tenant_input := object.union(base_input, {
	"tenant": {
		"present": true,
		"legacy": true,
		"organization_id": "10000000-0000-0000-0000-000000000001",
		"membership_id": "20000000-0000-0000-0000-000000000001",
		"organization_status": "initializing",
		"membership_status": "active",
		"organization_policy_revision": 7,
		"membership_security_revision": 11,
	},
	"tenant_library_ids": [1, 2, 3, 4, 5, 7],
})

test_bloem_tenant_input_is_valid if {
	tenant_valid(bloem_tenant_input)
}

test_bloem_tenant_validation_rejects_malformed_status if {
	not tenant_valid(object.union(bloem_tenant_input, {
		"tenant": object.union(bloem_tenant_input.tenant, {"organization_status": "broken"}),
	}))
}

test_bloem_tenant_absent_keeps_silo_unrestricted if {
	got := decision with input as object.union(base_input, {"tenant_library_ids": [1, 2]})
	got.unrestricted
	got.allowed_library_ids == []
}

test_bloem_tenant_scope_intersects_unrestricted_profile if {
	got := decision with input as object.union(bloem_tenant_input, {"tenant_library_ids": [10, 20]})
	not got.unrestricted
	got.allowed_library_ids == [10, 20]
}

test_bloem_no_profile_is_bounded_by_tenant if {
	got := decision with input as bloem_tenant_input
	not got.unrestricted
	got.libraries_restricted
	got.allowed_library_ids == [1, 2, 3, 4, 5, 7]
	got.disabled_library_ids == []
}

test_bloem_tenant_bounds_account_restriction if {
	got := decision with input as object.union(bloem_tenant_input, {
		"account_restricted": true,
		"account_library_ids": [99, 3, 1, 3],
	})
	not got.unrestricted
	got.allowed_library_ids == [1, 3]
}

test_bloem_tenant_bounds_profile_restriction if {
	got := decision with input as object.union(bloem_tenant_input, {
		"profile_id": "prof-1",
		"profile_present": true,
		"profile_library_restricted": true,
		"profile_allowed_library_ids": [99, 4, 2, 2],
	})
	not got.unrestricted
	got.allowed_library_ids == [2, 4]
}

test_bloem_hidden_reports_tenant_libraries_when_otherwise_unrestricted if {
	got := decision with input as object.union(bloem_tenant_input, {"disabled_library_ids": [2, 9]})
	got.hidden_library_ids == [2]
}

test_bloem_disabled_subtracts_from_tenant_bound if {
	got := decision with input as object.union(bloem_tenant_input, {"disabled_library_ids": [2]})
	not got.unrestricted
	got.allowed_library_ids == [1, 3, 4, 5, 7]
	got.disabled_library_ids == []
}

bloem_tenant_widening_override(_, _) := {
	"unrestricted": false,
	"allowed_library_ids": [10, 99],
}

test_bloem_tenant_scope_cannot_be_widened_by_custom_policy if {
	got := decision
		with input as object.union(bloem_tenant_input, {"tenant_library_ids": [10]})
		with data.silo_custom.scope.override as bloem_tenant_widening_override
	not got.unrestricted
	got.allowed_library_ids == [10]
}
