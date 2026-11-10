import { useBloemT } from "@/bloem/i18n";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type {
  OrganizationGroup,
  OrganizationPerson,
} from "@/hooks/queries/admin/organizationPeople";

export default function PeopleTable({
  people,
  groups,
  changingProfileId,
  groupDrafts,
  onInspect,
  onChangeGroup,
}: {
  people: OrganizationPerson[];
  groups: OrganizationGroup[];
  changingProfileId?: string;
  groupDrafts: Record<string, number>;
  onInspect(person: OrganizationPerson): void;
  onChangeGroup(person: OrganizationPerson, profileId: string, groupId: number): void;
}) {
  const bt = useBloemT();
  return (
    <>
      <div className="surface-panel hidden overflow-x-auto rounded-2xl md:block">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{bt("bloem.admin.people.table.person")}</TableHead>
              <TableHead>{bt("bloem.admin.people.table.status")}</TableHead>
              <TableHead>{bt("bloem.admin.people.table.profilesAndAccessGroups")}</TableHead>
              <TableHead>{bt("bloem.admin.people.table.lastActivity")}</TableHead>
              <TableHead className="text-end">
                <span className="sr-only">{bt("bloem.admin.people.table.details")}</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {people.map((person) => (
              <TableRow key={person.account_id}>
                <TableCell>
                  <div className="font-medium">{person.display_name || person.email}</div>
                  <div className="text-muted-foreground text-xs">{person.email}</div>
                </TableCell>
                <TableCell>
                  <Badge variant="outline" className="capitalize">
                    {bt("bloem.admin.people.membershipStatus", {
                      status: person.membership_status,
                    })}
                  </Badge>
                </TableCell>
                <TableCell>
                  <ProfileGroups
                    person={person}
                    groups={groups}
                    changingProfileId={changingProfileId}
                    groupDrafts={groupDrafts}
                    onChangeGroup={onChangeGroup}
                  />
                </TableCell>
                <TableCell className="text-muted-foreground text-sm">
                  {new Date(person.last_activity).toLocaleDateString()}
                </TableCell>
                <TableCell className="text-end">
                  <Button
                    variant="ghost"
                    size="sm"
                    aria-label={bt("bloem.admin.people.table.detailsFor", {
                      person: person.display_name || person.email,
                    })}
                    onClick={() => onInspect(person)}
                  >
                    {bt("bloem.admin.people.table.details")}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      <ul className="space-y-3 md:hidden" aria-label={bt("bloem.admin.people.table.people")}>
        {people.map((person) => (
          <li key={person.account_id} className="surface-panel rounded-2xl p-4">
            <div className="flex items-start justify-between gap-3">
              <div>
                <div className="font-medium">{person.display_name || person.email}</div>
                <div className="text-muted-foreground text-xs">{person.email}</div>
              </div>
              <Badge variant="outline" className="capitalize">
                {bt("bloem.admin.people.membershipStatus", { status: person.membership_status })}
              </Badge>
            </div>
            <div className="mt-4">
              <ProfileGroups
                person={person}
                groups={groups}
                changingProfileId={changingProfileId}
                groupDrafts={groupDrafts}
                onChangeGroup={onChangeGroup}
              />
            </div>
            <Button
              className="mt-3"
              variant="outline"
              size="sm"
              aria-label={bt("bloem.admin.people.table.detailsFor", {
                person: person.display_name || person.email,
              })}
              onClick={() => onInspect(person)}
            >
              {bt("bloem.admin.people.table.details")}
            </Button>
          </li>
        ))}
      </ul>
    </>
  );
}

function ProfileGroups({
  person,
  groups,
  changingProfileId,
  groupDrafts,
  onChangeGroup,
}: {
  person: OrganizationPerson;
  groups: OrganizationGroup[];
  changingProfileId?: string;
  groupDrafts: Record<string, number>;
  onChangeGroup(person: OrganizationPerson, profileId: string, groupId: number): void;
}) {
  const bt = useBloemT();
  return (
    <div className="space-y-2">
      {person.profiles.map((profile) => (
        <label key={profile.id} className="flex items-center gap-2 text-xs">
          <span className="min-w-20 truncate">{profile.name}</span>
          <span className="sr-only">
            {bt("bloem.admin.people.table.groupForProfile", { name: profile.name })}
          </span>
          <select
            aria-label={bt("bloem.admin.people.table.groupForProfile", { name: profile.name })}
            className="border-input bg-background h-8 min-w-28 rounded-md border px-2"
            value={groupDrafts[profile.id] ?? profile.group_id}
            disabled={changingProfileId === profile.id || groups.length === 0}
            onChange={(event) => onChangeGroup(person, profile.id, Number(event.target.value))}
          >
            {groups.length === 0 ? (
              <option value={profile.group_id}>{profile.group_name}</option>
            ) : null}
            {groups.map((group) => (
              <option key={group.id} value={group.id}>
                {group.name}
              </option>
            ))}
          </select>
        </label>
      ))}
    </div>
  );
}
