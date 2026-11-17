package silo.scope

import rego.v1

# Bloem tenancy is opt-in on the viewer resolver. With a tenant present every
# scope is bounded by the tenant's libraries, so an otherwise unrestricted
# profile reports the tenant libraries it hid. scope.rego's library_decision
# takes this branch first.

bloem_tenant_library_decision(i) := result if {
	effective := effective_libraries(i)
	allowed := bloem_tenant_bounded_libraries(i, effective)
	result := {
		"unrestricted": false,
		"allowed_library_ids": subtract(allowed, disabled_ids(i)),
		"disabled_library_ids": [],
		"hidden_library_ids": intersect(allowed, disabled_ids(i)),
		"libraries_restricted": true,
	}
}

bloem_tenant_present(i) if {
	object.get(object.get(i, "tenant", {}), "present", false) == true
}

bloem_tenant_bounded_libraries(i, effective) := bloem_tenant_library_ids(i) if {
	effective.unrestricted
} else := intersect(bloem_tenant_library_ids(i), effective.allowed_library_ids)

bloem_tenant_library_ids(i) := unique_sorted(ids) if {
	ids := object.get(i, "tenant_library_ids", [])
	is_array(ids)
} else := []
