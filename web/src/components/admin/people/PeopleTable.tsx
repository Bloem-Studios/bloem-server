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
  onInspect,
  onChangeGroup,
}: {
  people: OrganizationPerson[];
  groups: OrganizationGroup[];
  changingProfileId?: string;
  onInspect(person: OrganizationPerson): void;
  onChangeGroup(person: OrganizationPerson, profileId: string, groupId: number): void;
}) {
  return (
    <>
      <div className="surface-panel hidden overflow-x-auto rounded-2xl md:block">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Account</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Profiles and groups</TableHead>
              <TableHead>Last activity</TableHead>
              <TableHead className="text-right">Details</TableHead>
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
                  <Badge variant="outline">{person.membership_status}</Badge>
                </TableCell>
                <TableCell>
                  <ProfileGroups
                    person={person}
                    groups={groups}
                    changingProfileId={changingProfileId}
                    onChangeGroup={onChangeGroup}
                  />
                </TableCell>
                <TableCell className="text-muted-foreground text-sm">
                  {new Date(person.last_activity).toLocaleDateString()}
                </TableCell>
                <TableCell className="text-right">
                  <Button variant="ghost" size="sm" onClick={() => onInspect(person)}>
                    Inspect {person.display_name || person.email}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      <ul className="space-y-3 md:hidden" aria-label="People">
        {people.map((person) => (
          <li key={person.account_id} className="surface-panel rounded-2xl p-4">
            <div className="flex items-start justify-between gap-3">
              <div>
                <div className="font-medium">{person.display_name || person.email}</div>
                <div className="text-muted-foreground text-xs">{person.email}</div>
              </div>
              <Badge variant="outline">{person.membership_status}</Badge>
            </div>
            <div className="mt-4">
              <ProfileGroups
                person={person}
                groups={groups}
                changingProfileId={changingProfileId}
                onChangeGroup={onChangeGroup}
              />
            </div>
            <Button className="mt-3" variant="outline" size="sm" onClick={() => onInspect(person)}>
              Inspect {person.display_name || person.email}
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
  onChangeGroup,
}: {
  person: OrganizationPerson;
  groups: OrganizationGroup[];
  changingProfileId?: string;
  onChangeGroup(person: OrganizationPerson, profileId: string, groupId: number): void;
}) {
  return (
    <div className="space-y-2">
      {person.profiles.map((profile) => (
        <label key={profile.id} className="flex items-center gap-2 text-xs">
          <span className="min-w-20 truncate">{profile.name}</span>
          <span className="sr-only">Group for {profile.name} profile</span>
          <select
            aria-label={`Group for ${profile.name} profile`}
            className="border-input bg-background h-8 min-w-28 rounded-md border px-2"
            value={profile.group_id}
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
