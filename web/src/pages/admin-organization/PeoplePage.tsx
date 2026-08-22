import { useEffect, useMemo, useRef, useState } from "react";
import { ChevronLeft, ChevronRight, Search, Users } from "lucide-react";
import { useSearchParams } from "react-router";
import { useQueryClient } from "@tanstack/react-query";

import { AdminV2ClientError } from "@/api/adminV2Client";
import BulkJobResult from "@/components/admin/people/BulkJobResult";
import BulkPeopleActionBar from "@/components/admin/people/BulkPeopleActionBar";
import BulkPolicyDrawer from "@/components/admin/people/BulkPolicyDrawer";
import PeopleTable from "@/components/admin/people/PeopleTable";
import PersonDetailSheet from "@/components/admin/people/PersonDetailSheet";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import { useOrganizationEntitlementCohorts } from "@/hooks/queries/admin/entitlementCohorts";
import {
  peopleFiltersFromSearch,
  peopleFiltersToSearch,
  organizationPeopleKeys,
  useCreatePeopleBulkJob,
  useCreatePeopleSelection,
  useOrganizationGroups,
  useOrganizationPeople,
  usePeopleBulkJob,
  useRefreshOrganizationPerson,
  useUpdateProfileGroup,
  type OrganizationPerson,
  type PeopleBulkJob,
  type PeopleFilters,
  type PeopleSelection,
} from "@/hooks/queries/admin/organizationPeople";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

type PendingAction = {
  kind: "assign_group" | "suspend_memberships" | "reactivate_memberships";
  groupId?: number;
};

type ProfileConflict = {
  accountId: number;
  profileId: string;
  intendedGroupId: number;
  currentRevision: number;
  intendedGroup: string;
  currentGroup: string;
  message: string;
};

export default function PeoplePage() {
  useDocumentTitle("People");
  const { active } = useAdminContext();
  const contextKey = active?.key ?? "organization:unavailable";
  const [searchParams, setSearchParams] = useSearchParams();
  const filters = useMemo(() => peopleFiltersFromSearch(searchParams), [searchParams]);
  const filterSignature = JSON.stringify({ ...filters, cursor: undefined });
  const [searchInput, setSearchInput] = useState(filters.query);
  const [selection, setSelection] = useState<PeopleSelection | null>(null);
  const [inspected, setInspected] = useState<OrganizationPerson | null>(null);
  const [pendingAction, setPendingAction] = useState<PendingAction | null>(null);
  const [policyDrawerOpen, setPolicyDrawerOpen] = useState(false);
  const [submittedJob, setSubmittedJob] = useState<PeopleBulkJob | null>(null);
  const [changingProfileId, setChangingProfileId] = useState<string>();
  const [groupDrafts, setGroupDrafts] = useState<Record<string, number>>({});
  const [profileConflict, setProfileConflict] = useState<ProfileConflict | null>(null);
  const [cursorHistory, setCursorHistory] = useState<(string | undefined)[]>(() =>
    filters.cursor ? [undefined, filters.cursor] : [undefined],
  );
  const previousFilter = useRef(filterSignature);
  const previousContext = useRef(contextKey);
  const refreshedTerminalJob = useRef<string | undefined>(undefined);
  const queryClient = useQueryClient();

  const people = useOrganizationPeople(contextKey, filters);
  const groups = useOrganizationGroups(contextKey);
  const cohorts = useOrganizationEntitlementCohorts(contextKey, false);
  const createSelection = useCreatePeopleSelection(contextKey);
  const createJob = useCreatePeopleBulkJob(contextKey);
  const polledJob = usePeopleBulkJob(contextKey, submittedJob?.job_id);
  const updateProfile = useUpdateProfileGroup(contextKey);
  const refreshPerson = useRefreshOrganizationPerson(contextKey);
  const visibleJob =
    previousContext.current === contextKey ? (polledJob.data ?? submittedJob) : null;

  useEffect(() => {
    if (previousFilter.current !== filterSignature) {
      previousFilter.current = filterSignature;
      setSelection(null);
      setPendingAction(null);
      setPolicyDrawerOpen(false);
      setSubmittedJob(null);
      setCursorHistory(filters.cursor ? [undefined, filters.cursor] : [undefined]);
      setProfileConflict(null);
      setGroupDrafts({});
    }
  }, [filterSignature, filters.cursor]);

  useEffect(() => {
    if (previousContext.current !== contextKey) {
      previousContext.current = contextKey;
      setSelection(null);
      setPendingAction(null);
      setPolicyDrawerOpen(false);
      setSubmittedJob(null);
      setInspected(null);
      setCursorHistory([undefined]);
      setProfileConflict(null);
      setGroupDrafts({});
      refreshedTerminalJob.current = undefined;
    }
  }, [contextKey]);

  useEffect(() => {
    setSearchInput(filters.query);
  }, [filters.query]);

  useEffect(() => {
    if (!filters.cursor) return;
    setCursorHistory((history) =>
      history.includes(filters.cursor) ? history : [...history, filters.cursor],
    );
  }, [filters.cursor]);

  useEffect(() => {
    if (
      visibleJob &&
      (visibleJob.status === "completed" || visibleJob.status === "failed") &&
      refreshedTerminalJob.current !== visibleJob.job_id
    ) {
      refreshedTerminalJob.current = visibleJob.job_id;
      void queryClient.invalidateQueries({
        queryKey: organizationPeopleKeys.peopleRoot(contextKey),
      });
    }
  }, [contextKey, queryClient, visibleJob]);

  function updateFilters(change: Partial<PeopleFilters>) {
    setSearchParams(peopleFiltersToSearch({ ...filters, ...change, cursor: undefined }));
  }

  async function selectAll() {
    const created = await createSelection.mutateAsync({ ...filters, cursor: undefined });
    setSelection(created);
  }

  async function startBulkJob() {
    if (!selection || !pendingAction) return;
    const job = await createJob.mutateAsync({
      selectionToken: selection.token,
      ...pendingAction,
    });
    refreshedTerminalJob.current = undefined;
    setSubmittedJob(job);
    setPendingAction(null);
  }

  async function changeGroup(person: OrganizationPerson, profileId: string, groupId: number) {
    const intendedGroup =
      groups.data?.find((group) => group.id === groupId)?.name ?? `Group ${groupId}`;
    setGroupDrafts((drafts) => ({ ...drafts, [profileId]: groupId }));
    setProfileConflict(null);
    setChangingProfileId(profileId);
    try {
      await updateProfile.mutateAsync({
        accountId: person.account_id,
        profileId,
        expectedRevision: person.security_revision,
        groupId,
      });
      setGroupDrafts((drafts) => {
        const next = { ...drafts };
        delete next[profileId];
        return next;
      });
    } catch (error) {
      if (error instanceof AdminV2ClientError && error.status === 409) {
        try {
          const current = await refreshPerson(person.account_id);
          const currentProfile = current.profiles.find((profile) => profile.id === profileId);
          setProfileConflict({
            accountId: current.account_id,
            profileId,
            intendedGroupId: groupId,
            currentRevision: current.security_revision,
            intendedGroup,
            currentGroup: currentProfile?.group_name ?? "unknown",
            message:
              "This profile changed on the server. Review the current group before retrying.",
          });
        } catch {
          setProfileConflict({
            accountId: person.account_id,
            profileId,
            intendedGroupId: groupId,
            currentRevision: person.security_revision,
            intendedGroup,
            currentGroup: "could not be reloaded",
            message:
              "This profile changed on the server, but its current state could not be loaded.",
          });
        }
      } else {
        setProfileConflict({
          accountId: person.account_id,
          profileId,
          intendedGroupId: groupId,
          currentRevision: person.security_revision,
          intendedGroup,
          currentGroup:
            person.profiles.find((profile) => profile.id === profileId)?.group_name ?? "unknown",
          message:
            error instanceof Error ? error.message : "The profile group could not be changed.",
        });
      }
    } finally {
      setChangingProfileId(undefined);
    }
  }

  const resultCount = people.data?.approximate_total ?? 0;
  return (
    <section className="admin-page space-y-6">
      <div className="page-header">
        <div className="space-y-2">
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
            People
          </h1>
          <p className="page-subtitle">
            Memberships, profiles, and access groups in {active?.name ?? "this organization"}.
          </p>
        </div>
        {resultCount > 0 ? (
          <Button
            variant="outline"
            onClick={() => void selectAll()}
            disabled={createSelection.isPending}
          >
            {createSelection.isPending
              ? "Creating selection…"
              : `Select all ${resultCount.toLocaleString()} results`}
          </Button>
        ) : null}
      </div>

      <form
        role="search"
        className="surface-panel grid gap-3 rounded-2xl p-4 md:grid-cols-2 xl:grid-cols-[minmax(0,1fr)_10rem_11rem_11rem_12rem_auto]"
        onSubmit={(event) => {
          event.preventDefault();
          updateFilters({ query: searchInput.trim() });
        }}
      >
        <div className="relative">
          <Search className="text-muted-foreground absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2" />
          <Input
            type="search"
            aria-label="Search people"
            placeholder="Email, display name, or profile"
            value={searchInput}
            onChange={(event) => setSearchInput(event.target.value)}
            className="pl-9"
          />
        </div>
        <select
          aria-label="Membership status"
          className="border-input bg-background h-9 rounded-md border px-3 text-sm"
          value={filters.status[0] ?? "all"}
          onChange={(event) =>
            updateFilters({
              status:
                event.target.value === "all"
                  ? []
                  : [event.target.value as "active" | "suspended" | "invited"],
            })
          }
        >
          <option value="all">All statuses</option>
          <option value="active">Active</option>
          <option value="suspended">Suspended</option>
          <option value="invited">Invited</option>
        </select>
        <select
          aria-label="Access group"
          className="border-input bg-background h-9 rounded-md border px-3 text-sm"
          value={filters.groupIds[0] ?? "all"}
          onChange={(event) =>
            updateFilters({
              groupIds: event.target.value === "all" ? [] : [Number(event.target.value)],
            })
          }
        >
          <option value="all">All access groups</option>
          {(groups.data ?? []).map((group) => (
            <option key={group.id} value={group.id}>
              {group.name}
            </option>
          ))}
        </select>
        <Input
          type="date"
          aria-label="Recent activity since"
          value={filters.activeSince?.slice(0, 10) ?? ""}
          onChange={(event) =>
            updateFilters({
              activeSince: event.target.value
                ? new Date(`${event.target.value}T00:00:00.000Z`).toISOString()
                : undefined,
            })
          }
        />
        <select
          aria-label="Sort people"
          className="border-input bg-background h-9 rounded-md border px-3 text-sm"
          value={filters.sort}
          onChange={(event) => updateFilters({ sort: event.target.value as PeopleFilters["sort"] })}
        >
          <option value="name">Name</option>
          <option value="email">Email</option>
          <option value="recent_activity">Recent activity</option>
        </select>
        <Button type="submit">Search</Button>
      </form>

      {profileConflict ? (
        <div
          className="border-destructive/30 bg-destructive/10 text-destructive rounded-xl border p-4 text-sm"
          role="alert"
        >
          {profileConflict.message} Intended: {profileConflict.intendedGroup}. Current:{" "}
          {profileConflict.currentGroup}.
          <Button
            className="ml-3"
            size="sm"
            variant="outline"
            disabled={changingProfileId === profileConflict.profileId}
            onClick={() => {
              const conflict = profileConflict;
              setProfileConflict(null);
              setChangingProfileId(conflict.profileId);
              void updateProfile
                .mutateAsync({
                  accountId: conflict.accountId,
                  profileId: conflict.profileId,
                  expectedRevision: conflict.currentRevision,
                  groupId: conflict.intendedGroupId,
                })
                .then(() =>
                  setGroupDrafts((drafts) => {
                    const next = { ...drafts };
                    delete next[conflict.profileId];
                    return next;
                  }),
                )
                .catch((error: unknown) =>
                  setProfileConflict({
                    ...conflict,
                    message:
                      error instanceof Error ? error.message : "The retry could not be completed.",
                  }),
                )
                .finally(() => setChangingProfileId(undefined));
            }}
          >
            Retry group change
          </Button>
        </div>
      ) : null}

      {people.isLoading ? (
        <div className="space-y-3" role="status" aria-label="Loading people">
          {Array.from({ length: 6 }, (_, index) => (
            <Skeleton key={index} className="h-16 w-full" />
          ))}
        </div>
      ) : people.isError ? (
        <div
          className="border-destructive/30 bg-destructive/10 text-destructive rounded-xl border p-4"
          role="alert"
        >
          {people.error.message}
        </div>
      ) : (people.data?.items.length ?? 0) === 0 ? (
        <div className="surface-panel text-muted-foreground rounded-2xl p-10 text-center">
          <Users className="mx-auto mb-3 h-8 w-8" />
          No people match these filters.
        </div>
      ) : (
        <PeopleTable
          people={people.data?.items ?? []}
          groups={groups.data ?? []}
          changingProfileId={changingProfileId}
          groupDrafts={groupDrafts}
          onInspect={setInspected}
          onChangeGroup={(person, profileId, groupId) =>
            void changeGroup(person, profileId, groupId)
          }
        />
      )}

      <nav className="flex items-center justify-between" aria-label="People pages">
        <Button
          variant="outline"
          disabled={!filters.cursor || cursorHistory.length <= 1}
          onClick={() => {
            const currentIndex = cursorHistory.lastIndexOf(filters.cursor);
            const previousCursor = cursorHistory[Math.max(0, currentIndex - 1)];
            setCursorHistory((history) => history.slice(0, Math.max(1, currentIndex)));
            setSearchParams(peopleFiltersToSearch({ ...filters, cursor: previousCursor }));
          }}
        >
          <ChevronLeft className="mr-2 h-4 w-4" /> Previous page
        </Button>
        <span className="text-muted-foreground text-sm">
          Approximately {resultCount.toLocaleString()} results
        </span>
        <Button
          variant="outline"
          disabled={!people.data?.next_cursor}
          onClick={() => {
            const nextCursor = people.data?.next_cursor;
            if (!nextCursor) return;
            setCursorHistory((history) => [...history, nextCursor]);
            setSearchParams(peopleFiltersToSearch({ ...filters, cursor: nextCursor }));
          }}
        >
          Next page <ChevronRight className="ml-2 h-4 w-4" />
        </Button>
      </nav>

      {selection ? (
        <BulkPeopleActionBar
          selection={selection}
          groups={groups.data ?? []}
          onApplyPolicy={() => setPolicyDrawerOpen(true)}
          onAction={(kind, groupId) => setPendingAction({ kind, groupId })}
        />
      ) : null}
      {visibleJob ? <BulkJobResult job={visibleJob} /> : null}
      {selection ? (
        <BulkPolicyDrawer
          open={policyDrawerOpen}
          contextKey={contextKey}
          organizationName={active?.name ?? "this organization"}
          selection={selection}
          cohorts={cohorts.data ?? []}
          initialCohortID={searchParams.get("policy_cohort") || undefined}
          onOpenChange={setPolicyDrawerOpen}
          onRetrySelection={() => void selectAll()}
        />
      ) : null}
      <PersonDetailSheet person={inspected} onOpenChange={(open) => !open && setInspected(null)} />

      <AlertDialog
        open={Boolean(pendingAction)}
        onOpenChange={(open) => !open && setPendingAction(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Confirm bulk people action</AlertDialogTitle>
            <AlertDialogDescription>
              {pendingAction ? actionLabel(pendingAction, groups.data ?? []) : "Apply this action"}{" "}
              in {active?.name ?? "this organization"}. The immutable selection contains{" "}
              {selection?.matched.toLocaleString() ?? 0} matched and{" "}
              {selection?.excluded.toLocaleString() ?? 0} excluded. Filters:{" "}
              {filterSummary(filters, groups.data ?? [])}.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={() => void startBulkJob()} disabled={createJob.isPending}>
              Start bulk job
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}

function actionLabel(action: PendingAction, groups: { id: number; name: string }[]): string {
  if (action.kind === "assign_group") {
    const group = groups.find((candidate) => candidate.id === action.groupId);
    return `Assign the selected profiles to ${group?.name ?? `group #${action.groupId}`}`;
  }
  if (action.kind === "reactivate_memberships") return "Reactivate selected memberships";
  return "Suspend selected memberships";
}

function filterSummary(filters: PeopleFilters, groups: { id: number; name: string }[]): string {
  const selectedGroups = filters.groupIds.map(
    (id) => groups.find((group) => group.id === id)?.name ?? `#${id}`,
  );
  const parts = [
    filters.query ? `search “${filters.query}”` : "all people",
    filters.status.length ? `status ${filters.status.join(", ")}` : "all statuses",
    selectedGroups.length ? `group ${selectedGroups.join(", ")}` : "all groups",
    filters.activeSince ? `active since ${filters.activeSince.slice(0, 10)}` : "any activity date",
    `sort ${filters.sort.replace(/_/g, " ")}`,
  ];
  return parts.join("; ");
}
