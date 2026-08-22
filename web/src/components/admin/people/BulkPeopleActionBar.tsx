import { Button } from "@/components/ui/button";
import type { OrganizationGroup, PeopleSelection } from "@/hooks/queries/admin/organizationPeople";

export default function BulkPeopleActionBar({
  selection,
  groups,
  onApplyPolicy,
  onAction,
}: {
  selection: PeopleSelection;
  groups: OrganizationGroup[];
  onApplyPolicy(): void;
  onAction(
    kind: "assign_group" | "suspend_memberships" | "reactivate_memberships",
    groupId?: number,
  ): void;
}) {
  return (
    <aside
      className="border-primary/20 bg-primary/5 sticky bottom-4 z-20 flex flex-wrap items-center gap-2 rounded-2xl border p-3 shadow-lg backdrop-blur"
      aria-label="Bulk people actions"
    >
      <strong className="mr-auto text-sm">
        {selection.matched.toLocaleString()} people selected
      </strong>
      <Button size="sm" onClick={onApplyPolicy}>
        Apply policy
      </Button>
      {groups.length > 0 ? (
        <select
          aria-label="Assign selected people to group"
          className="border-input bg-background h-9 rounded-md border px-3 text-sm"
          defaultValue=""
          onChange={(event) => {
            const groupId = Number(event.target.value);
            if (groupId > 0) onAction("assign_group", groupId);
            event.target.value = "";
          }}
        >
          <option value="">Assign group…</option>
          {groups.map((group) => (
            <option key={group.id} value={group.id}>
              {group.name}
            </option>
          ))}
        </select>
      ) : null}
      <Button size="sm" variant="outline" onClick={() => onAction("reactivate_memberships")}>
        Reactivate memberships
      </Button>
      <Button size="sm" variant="destructive" onClick={() => onAction("suspend_memberships")}>
        Suspend memberships
      </Button>
    </aside>
  );
}
