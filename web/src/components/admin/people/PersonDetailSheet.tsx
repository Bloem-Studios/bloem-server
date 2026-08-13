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
                <Badge variant="outline">{person.membership_status}</Badge>
                <Badge variant="secondary">{person.legacy_role}</Badge>
              </div>
              <div>
                <h3 className="text-sm font-semibold">Profiles</h3>
                <ul className="mt-2 space-y-2">
                  {person.profiles.map((profile) => (
                    <li key={profile.id} className="surface-panel rounded-xl p-3 text-sm">
                      <span className="font-medium">{profile.name}</span>
                      <span className="text-muted-foreground ml-2">{profile.group_name}</span>
                    </li>
                  ))}
                </ul>
              </div>
              <dl className="grid grid-cols-2 gap-3 text-sm">
                <dt className="text-muted-foreground">Security revision</dt>
                <dd>{person.security_revision}</dd>
                <dt className="text-muted-foreground">Last activity</dt>
                <dd>{new Date(person.last_activity).toLocaleString()}</dd>
              </dl>
            </div>
          </>
        ) : null}
      </SheetContent>
    </Sheet>
  );
}
