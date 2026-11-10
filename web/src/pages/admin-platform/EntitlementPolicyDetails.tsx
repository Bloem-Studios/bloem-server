import type { EntitlementTemplatePolicy } from "@/api/bloemTypes";
import { useBloemT } from "@/bloem/i18n";
import { policyRows } from "@/components/admin/policy/policyVocabulary";
import { useAdminLibraries } from "@/hooks/queries/admin/libraries";

export function EntitlementPolicyDetails({ policy }: { policy: EntitlementTemplatePolicy }) {
  const bt = useBloemT();
  const libraries = useAdminLibraries();
  return (
    <dl className="text-muted-foreground grid gap-x-6 gap-y-1 text-xs sm:grid-cols-2">
      {policyRows(policy, libraries.data ?? []).map(([label, value]) => (
        <div key={label} className="flex min-w-0 gap-1">
          <dt>{bt("bloem.admin.plans.policyDetails.term", { label })}</dt>
          <dd className="text-foreground break-words">{value}</dd>
        </div>
      ))}
    </dl>
  );
}
