import { useBloemT } from "@/bloem/i18n";
import { Badge } from "@/components/ui/badge";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import type { OrganizationPerson } from "@/hooks/queries/admin/organizationPeople";

export default function PersonDetailSheet({
  person,
  onOpenChange,
}: {
  person: OrganizationPerson | null;
  onOpenChange(open: boolean): void;
}) {
  const bt = useBloemT();
  return (
    <Sheet open={Boolean(person)} onOpenChange={onOpenChange}>
      <SheetContent className="overflow-y-auto sm:max-w-lg">
        {person ? (
          <>
            <SheetHeader>
              <SheetTitle>{person.display_name || person.email}</SheetTitle>
              <SheetDescription>{person.email}</SheetDescription>
            </SheetHeader>
            <div className="space-y-5 px-4 pb-6">
              <div className="flex gap-2">
                <Badge variant="outline" className="capitalize">
                  {person.membership_status}
                </Badge>
                <Badge variant="secondary" className="capitalize">
                  {person.legacy_role}
                </Badge>
              </div>
              <div>
                <h3 className="text-sm font-medium">
                  {bt("bloem.admin.people.detail.profilesAndAccessGroups")}
                </h3>
                <ul className="mt-2 space-y-2">
                  {person.profiles.map((profile) => (
                    <li key={profile.id} className="surface-panel rounded-xl p-3 text-sm">
                      <span className="font-medium">{profile.name}</span>
                      <span className="text-muted-foreground ms-2">{profile.group_name}</span>
                    </li>
                  ))}
                </ul>
              </div>
              <dl className="grid grid-cols-2 gap-3 text-sm">
                <dt className="text-muted-foreground">
                  {bt("bloem.admin.people.detail.lastActive")}
                </dt>
                <dd>{new Date(person.last_activity).toLocaleString()}</dd>
                <dt className="text-muted-foreground">{bt("bloem.admin.people.detail.account")}</dt>
                <dd className="font-mono text-xs">
                  {bt("bloem.admin.people.detail.revision", {
                    accountId: person.account_id,
                    securityRevision: person.security_revision,
                  })}
                </dd>
              </dl>
            </div>
          </>
        ) : null}
      </SheetContent>
    </Sheet>
  );
}
