import { useEffect, useMemo, useRef, useState } from "react";
import { ChevronLeft, ChevronRight, Search, Users } from "lucide-react";
import { useSearchParams } from "react-router";

import BulkJobResult from "@/components/admin/people/BulkJobResult";
import BulkPeopleActionBar from "@/components/admin/people/BulkPeopleActionBar";
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
import {
  peopleFiltersFromSearch,
  peopleFiltersToSearch,
  useCreatePeopleBulkJob,
  useCreatePeopleSelection,
  useOrganizationGroups,
  useOrganizationPeople,
  usePeopleBulkJob,
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
  const [submittedJob, setSubmittedJob] = useState<PeopleBulkJob | null>(null);
  const [changingProfileId, setChangingProfileId] = useState<string>();
  const previousFilter = useRef(filterSignature);
  const previousContext = useRef(contextKey);

  const people = useOrganizationPeople(contextKey, filters);
  const groups = useOrganizationGroups(contextKey);
  const createSelection = useCreatePeopleSelection(contextKey);
  const createJob = useCreatePeopleBulkJob(contextKey);
  const polledJob = usePeopleBulkJob(contextKey, submittedJob?.job_id);
  const updateProfile = useUpdateProfileGroup(contextKey);
  const visibleJob =
    previousContext.current === contextKey ? (polledJob.data ?? submittedJob) : null;

  useEffect(() => {
    if (previousFilter.current !== filterSignature) {
      previousFilter.current = filterSignature;
      setSelection(null);
      setPendingAction(null);
      setSubmittedJob(null);
    }
  }, [filterSignature]);

  useEffect(() => {
    if (previousContext.current !== contextKey) {
      previousContext.current = contextKey;
      setSelection(null);
      setPendingAction(null);
      setSubmittedJob(null);
      setInspected(null);
    }
  }, [contextKey]);

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
    setSubmittedJob(job);
    setPendingAction(null);
  }

  async function changeGroup(person: OrganizationPerson, profileId: string, groupId: number) {
    setChangingProfileId(profileId);
    try {
      await updateProfile.mutateAsync({
        accountId: person.account_id,
        profileId,
        expectedRevision: person.security_revision,
        groupId,
      });
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
        className="surface-panel grid gap-3 rounded-2xl p-4 md:grid-cols-[minmax(0,1fr)_12rem_12rem_auto]"
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
          onInspect={setInspected}
          onChangeGroup={(person, profileId, groupId) =>
            void changeGroup(person, profileId, groupId)
          }
        />
      )}

      <nav className="flex items-center justify-between" aria-label="People pages">
        <Button
          variant="outline"
          disabled={!filters.cursor}
          onClick={() => setSearchParams(peopleFiltersToSearch({ ...filters, cursor: undefined }))}
        >
          <ChevronLeft className="mr-2 h-4 w-4" /> Previous page
        </Button>
        <span className="text-muted-foreground text-sm">
          Approximately {resultCount.toLocaleString()} results
        </span>
        <Button
          variant="outline"
          disabled={!people.data?.next_cursor}
          onClick={() =>
            setSearchParams(peopleFiltersToSearch({ ...filters, cursor: people.data?.next_cursor }))
          }
        >
          Next page <ChevronRight className="ml-2 h-4 w-4" />
        </Button>
      </nav>

      {selection ? (
        <BulkPeopleActionBar
          selection={selection}
          groups={groups.data ?? []}
          onAction={(kind, groupId) => setPendingAction({ kind, groupId })}
        />
      ) : null}
      {visibleJob ? <BulkJobResult job={visibleJob} /> : null}
      <PersonDetailSheet person={inspected} onOpenChange={(open) => !open && setInspected(null)} />

      <AlertDialog
        open={Boolean(pendingAction)}
        onOpenChange={(open) => !open && setPendingAction(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Confirm bulk people action</AlertDialogTitle>
            <AlertDialogDescription>
              {pendingAction ? actionLabel(pendingAction.kind) : "Apply this action"} in{" "}
              {active?.name ?? "this organization"}. The immutable selection contains{" "}
              {selection?.matched.toLocaleString() ?? 0} matched and{" "}
              {selection?.excluded.toLocaleString() ?? 0} excluded. Filters:{" "}
              {filterSummary(filters)}.
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

function actionLabel(kind: PendingAction["kind"]): string {
  if (kind === "assign_group") return "Assign the selected profiles to a group";
  if (kind === "reactivate_memberships") return "Reactivate selected memberships";
  return "Suspend selected memberships";
}

function filterSummary(filters: PeopleFilters): string {
  const parts = [
    filters.query ? `search “${filters.query}”` : "all people",
    filters.status.length ? `status ${filters.status.join(", ")}` : "all statuses",
    filters.groupIds.length ? `groups ${filters.groupIds.join(", ")}` : "all groups",
    `sort ${filters.sort.replace(/_/g, " ")}`,
  ];
  return parts.join("; ");
}
