// Bloem: one "before → after" line from a plan preview.
import { useBloemT } from "@/bloem/i18n";
import { formatPolicyValue, policyFieldLabel } from "./policyVocabulary";

export function PolicyValueChange({
  change,
}: {
  change: { field: string; before: unknown; after: unknown };
}) {
  const bt = useBloemT();
  const label = policyFieldLabel(change.field);
  return (
    <p className="text-muted-foreground text-sm">
      <span className="text-foreground font-medium">
        {label[0]?.toUpperCase() + label.slice(1)}
      </span>
      {bt("bloem.admin.policy.valueChange.policyValuePolicyValue2", {
        policyValue: formatPolicyValue(change.field, change.before),
        policyValue2: formatPolicyValue(change.field, change.after),
      })}
    </p>
  );
}
