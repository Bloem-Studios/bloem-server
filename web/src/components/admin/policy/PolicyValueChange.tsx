// Bloem: one "before → after" line from a plan preview.
import { formatPolicyValue, policyFieldLabel } from "./policyVocabulary";

export function PolicyValueChange({
  change,
}: {
  change: { field: string; before: unknown; after: unknown };
}) {
  const label = policyFieldLabel(change.field);
  return (
    <p className="text-muted-foreground text-sm">
      <span className="text-foreground font-medium">
        {label[0]?.toUpperCase() + label.slice(1)}
      </span>
      : {formatPolicyValue(change.field, change.before)} →{" "}
      {formatPolicyValue(change.field, change.after)}
    </p>
  );
}
