import { useBloemT } from "@/bloem/i18n";
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
  const bt = useBloemT();
  return (
    <aside
      className="border-primary/20 bg-primary/5 sticky bottom-4 z-20 flex flex-wrap items-center gap-2 rounded-2xl border p-3 shadow-lg backdrop-blur"
      aria-label={bt("bloem.admin.people.bulkBar.bulkPeopleActions")}
    >
      <strong className="me-auto text-sm">
        {bt("bloem.admin.people.bulkBar.selectedCount", { count: selection.matched })}
      </strong>
      <Button size="sm" onClick={onApplyPolicy}>
        {bt("bloem.admin.people.bulkBar.changePlanOrSettings")}
      </Button>
      {groups.length > 0 ? (
        <select
          aria-label={bt("bloem.admin.people.bulkBar.moveTheirProfilesToAn")}
          className="border-input bg-background h-9 rounded-md border px-3 text-sm"
          defaultValue=""
          onChange={(event) => {
            const groupId = Number(event.target.value);
            if (groupId > 0) onAction("assign_group", groupId);
            event.target.value = "";
          }}
        >
          <option value="">{bt("bloem.admin.people.bulkBar.moveToAccessGroup")}</option>
          {groups.map((group) => (
            <option key={group.id} value={group.id}>
              {group.name}
            </option>
          ))}
        </select>
      ) : null}
      <Button size="sm" variant="outline" onClick={() => onAction("reactivate_memberships")}>
        {bt("bloem.admin.people.bulkBar.reactivate")}
      </Button>
      <Button size="sm" variant="outline" onClick={() => onAction("suspend_memberships")}>
        {bt("bloem.admin.people.bulkBar.suspend")}
      </Button>
    </aside>
  );
}
