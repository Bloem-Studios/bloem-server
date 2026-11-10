import { useState, type ReactNode } from "react";
import { useBloemT } from "@/bloem/i18n";
import { AdminV2ClientError } from "@/api/adminV2Client";
import { ArrowLeft, ChevronLeft, ChevronRight, Plus, ShieldCheck, Users } from "lucide-react";
import { Link, useParams } from "react-router";
import { OrganizationLifecyclePanel } from "@/components/admin/organizations/OrganizationLifecyclePanel";
import { OrganizationEntitlementPanel } from "./OrganizationEntitlementPanel";
import { OrganizationBrandingPanel } from "./OrganizationBrandingPanel";
import { OrganizationDomainsPanel } from "./OrganizationDomainsPanel";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  useCreateOrganizationMembership,
  useOrganizationMemberships,
  usePlatformOrganization,
  useUpdateOrganizationMembership,
  type MembershipStatus,
  type OrganizationMembership,
  type PlatformOrganization,
} from "@/hooks/queries/admin/organizations";
import { AccountPicker } from "@/components/admin/policy/AccountPicker";
import { useAccountNames } from "@/components/admin/policy/useAccountNames";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

function mono(chunks: ReactNode[]) {
  return <span className="font-mono">{chunks}</span>;
}

function CountCard({ label, value }: { label: string; value: number }) {
  return (
    <Card className="gap-2 py-4">
      <CardContent className="px-4">
        <p className="text-muted-foreground text-xs font-medium tracking-wide uppercase">{label}</p>
        <p className="mt-1 text-2xl font-semibold tabular-nums">{value.toLocaleString()}</p>
      </CardContent>
    </Card>
  );
}

function AddMembershipDialog({
  organization,
  open,
  onOpenChange,
  onStale,
}: {
  organization: PlatformOrganization;
  open: boolean;
  onOpenChange(open: boolean): void;
  onStale(): Promise<void>;
}) {
  const bt = useBloemT();
  const mutation = useCreateOrganizationMembership(organization.id);
  const [accountId, setAccountId] = useState("");
  const [role, setRole] = useState<"admin" | "user">("user");
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const parsed = Number(accountId);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{bt("bloem.admin.platformOrganizations.addMember.title")}</DialogTitle>
          <DialogDescription>
            {bt("bloem.admin.platformOrganizations.addMember.description", {
              name: organization.name,
            })}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <AccountPicker
              id="membership-account-search"
              label={bt("bloem.admin.platformOrganizations.addMember.findAccount")}
              multiple={false}
              selected={/^[1-9]\d*$/.test(accountId) ? [Number(accountId)] : []}
              onChange={(ids) => setAccountId(ids[0] ? String(ids[0]) : "")}
            />
            <Label htmlFor="membership-account" className="text-muted-foreground text-xs">
              {bt("bloem.admin.platformOrganizations.addMember.accountNumber")}
            </Label>
            <Input
              className="sm:max-w-40"
              id="membership-account"
              inputMode="numeric"
              value={accountId}
              onChange={(e) => setAccountId(e.target.value)}
            />
            {fieldErrors.account_id ? (
              <p className="text-destructive text-sm">{fieldErrors.account_id}</p>
            ) : null}
          </div>
          <div className="space-y-2">
            <Label htmlFor="membership-role">
              {bt("bloem.admin.platformOrganizations.addMember.role")}
            </Label>
            <select
              id="membership-role"
              className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
              value={role}
              onChange={(e) => setRole(e.target.value as "admin" | "user")}
            >
              <option value="user">
                {bt("bloem.admin.platformOrganizations.addMember.member")}
              </option>
              <option value="admin">
                {bt("bloem.admin.platformOrganizations.addMember.organizationAdmin")}
              </option>
            </select>
          </div>
          {error || mutation.error ? (
            <p className="text-destructive text-sm" role="alert">
              {error ?? mutation.error?.message}
            </p>
          ) : null}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {bt("bloem.admin.platformOrganizations.addMember.cancel")}
          </Button>
          <Button
            disabled={!Number.isInteger(parsed) || parsed <= 0 || mutation.isPending}
            onClick={() =>
              void mutation
                .mutateAsync({
                  expected_revision: organization.policy_revision,
                  account_id: parsed,
                  legacy_role: role,
                  status: "active",
                })
                .then(() => onOpenChange(false))
                .catch(async (cause: unknown) => {
                  if (cause instanceof AdminV2ClientError) {
                    setFieldErrors(cause.fields);
                    if (cause.code === "authorization_state_changed") {
                      setError(bt("bloem.admin.platformOrganizations.addMember.stateChanged"));
                      await onStale();
                      return;
                    }
                  }
                  setError(
                    cause instanceof Error
                      ? cause.message
                      : bt("bloem.admin.platformOrganizations.addMember.failed"),
                  );
                })
            }
          >
            {mutation.isPending
              ? bt("bloem.admin.platformOrganizations.addMember.adding")
              : bt("bloem.admin.platformOrganizations.addMember.submit")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function MembershipRow({
  organization,
  membership,
  onStale,
}: {
  organization: PlatformOrganization;
  membership: OrganizationMembership;
  onStale(): Promise<void>;
}) {
  const bt = useBloemT();
  const update = useUpdateOrganizationMembership(organization.id);
  const [error, setError] = useState<string | null>(null);
  const isOwner = membership.account_id === organization.owner_account_id;
  function updateStatus(status: MembershipStatus) {
    setError(null);
    void update
      .mutateAsync({
        membership_id: membership.id,
        expected_revision: membership.security_revision,
        status,
      })
      .catch(async (cause: unknown) => {
        if (cause instanceof AdminV2ClientError && cause.code === "authorization_state_changed") {
          setError(bt("bloem.admin.platformOrganizations.members.changed"));
          await onStale();
          return;
        }
        setError(
          cause instanceof Error
            ? cause.message
            : bt("bloem.admin.platformOrganizations.members.updateFailed"),
        );
      });
  }
  const accountId = membership.account_id;
  return (
    <TableRow>
      <TableCell>
        <span className="font-medium">
          {membership.username ||
            bt("bloem.admin.platformOrganizations.members.accountNumber", { accountId })}
        </span>
        <span className="text-muted-foreground block text-xs">{membership.email}</span>
      </TableCell>
      <TableCell>
        {isOwner ? (
          <Badge variant="default">{bt("bloem.admin.platformOrganizations.members.owner")}</Badge>
        ) : (
          <Badge variant="outline">
            {membership.legacy_role === "admin"
              ? bt("bloem.admin.platformOrganizations.members.admin")
              : bt("bloem.admin.platformOrganizations.members.member")}
          </Badge>
        )}
      </TableCell>
      <TableCell>
        <Badge variant={membership.status === "active" ? "secondary" : "outline"}>
          {bt("bloem.admin.platformOrganizations.members.statusBadge", {
            status: membership.status,
          })}
        </Badge>
      </TableCell>
      <TableCell className="text-end">
        {error ? (
          <p className="text-destructive mb-2 text-xs" role="alert">
            {error}
          </p>
        ) : null}
        {membership.status === "active" ? (
          <Button
            size="sm"
            variant="outline"
            disabled={isOwner || update.isPending}
            aria-label={
              membership.username
                ? bt("bloem.admin.platformOrganizations.members.suspendNamed", {
                    name: membership.username,
                  })
                : bt("bloem.admin.platformOrganizations.members.suspendAccount", { accountId })
            }
            onClick={() => updateStatus("suspended")}
          >
            {bt("bloem.admin.platformOrganizations.members.suspend")}
          </Button>
        ) : (
          <Button
            size="sm"
            variant="outline"
            disabled={update.isPending}
            aria-label={
              membership.username
                ? bt("bloem.admin.platformOrganizations.members.reactivateNamed", {
                    name: membership.username,
                  })
                : bt("bloem.admin.platformOrganizations.members.reactivateAccount", { accountId })
            }
            onClick={() => updateStatus("active")}
          >
            {bt("bloem.admin.platformOrganizations.members.reactivate")}
          </Button>
        )}
      </TableCell>
    </TableRow>
  );
}

export default function OrganizationDetailPage() {
  const { id = "" } = useParams<{ id: string }>();
  const organization = usePlatformOrganization(id);
  const [membershipCursors, setMembershipCursors] = useState<string[]>([""]);
  const membershipCursor = membershipCursors[membershipCursors.length - 1] ?? "";
  const memberships = useOrganizationMemberships(id, membershipCursor);
  const [addMemberOpen, setAddMemberOpen] = useState(false);
  const bt = useBloemT();
  useDocumentTitle(organization.data?.name ?? bt("bloem.adminTitle.organization"));
  const accountName = useAccountNames();

  if (organization.isLoading)
    return (
      <div className="space-y-4" role="status">
        <Skeleton className="h-16 w-2/3" />
        <Skeleton className="h-64 w-full" />
      </div>
    );
  if (organization.isError || !organization.data)
    return (
      <div
        className="border-destructive/30 bg-destructive/10 text-destructive rounded-xl border p-4"
        role="alert"
      >
        {organization.error?.message ?? bt("bloem.admin.platformOrganizations.detail.notFound")}
      </div>
    );

  const current = organization.data;
  const items = memberships.data?.memberships ?? [];
  const activeMemberships = current.active_membership_count ?? 0;
  return (
    <section className="admin-page space-y-6">
      <div>
        <Button variant="ghost" size="sm" asChild>
          <Link to="/admin/platform/organizations">
            <ArrowLeft className="me-1 h-4 w-4" />
            {bt("bloem.admin.platformOrganizations.detail.back")}
          </Link>
        </Button>
      </div>
      <div className="page-header">
        <div className="space-y-2">
          <div className="flex flex-wrap items-center gap-3">
            <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
              {current.name}
            </h1>
            <Badge variant={current.status === "active" ? "default" : "secondary"}>
              {bt("bloem.admin.platformOrganizations.status.organization", {
                status: current.status,
              })}
            </Badge>
          </div>
          <p className="page-subtitle text-sm sm:text-base">
            {current.owner_account_id
              ? bt("bloem.admin.platformOrganizations.detail.slugAndOwner", {
                  slug: current.slug,
                  owner: accountName(current.owner_account_id),
                  mono,
                })
              : bt("bloem.admin.platformOrganizations.detail.slugNoOwner", {
                  slug: current.slug,
                  mono,
                })}
          </p>
        </div>
      </div>

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <CountCard
          label={bt("bloem.admin.platformOrganizations.detail.members")}
          value={current.membership_count ?? items.length}
        />
        <CountCard
          label={bt("bloem.admin.platformOrganizations.detail.profiles")}
          value={current.profile_count ?? 0}
        />
        <CountCard
          label={bt("bloem.admin.platformOrganizations.detail.ownLibraries")}
          value={current.library_count ?? 0}
        />
        <CountCard
          label={bt("bloem.admin.platformOrganizations.detail.sharedLibraries")}
          value={current.entitlement_count ?? 0}
        />
      </div>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <Card>
          <CardHeader className="border-b">
            <div className="flex items-center justify-between gap-3">
              <CardTitle className="flex items-center gap-2">
                <Users className="h-4 w-4" />
                {bt("bloem.admin.platformOrganizations.members.title")}
              </CardTitle>
              <Button size="sm" onClick={() => setAddMemberOpen(true)}>
                <Plus className="me-1 h-4 w-4" />
                {bt("bloem.admin.platformOrganizations.members.add")}
              </Button>
            </div>
          </CardHeader>
          <CardContent className="px-0">
            {memberships.isLoading ? (
              <div className="p-6 text-sm" role="status">
                {bt("bloem.admin.platformOrganizations.members.loading")}
              </div>
            ) : memberships.isError ? (
              <p className="text-destructive p-6 text-sm" role="alert">
                {memberships.error.message}
              </p>
            ) : items.length === 0 ? (
              <p className="text-muted-foreground p-6 text-sm">
                {bt("bloem.admin.platformOrganizations.members.empty")}
              </p>
            ) : (
              <div className="overflow-x-auto">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>
                        {bt("bloem.admin.platformOrganizations.members.account")}
                      </TableHead>
                      <TableHead>{bt("bloem.admin.platformOrganizations.members.role")}</TableHead>
                      <TableHead>
                        {bt("bloem.admin.platformOrganizations.members.status")}
                      </TableHead>
                      <TableHead>
                        <span className="sr-only">
                          {bt("bloem.admin.platformOrganizations.members.actions")}
                        </span>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {items.map((membership) => (
                      <MembershipRow
                        key={membership.id}
                        organization={current}
                        membership={membership}
                        onStale={async () => {
                          await Promise.all([organization.refetch(), memberships.refetch()]);
                        }}
                      />
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
            <div className="border-border flex items-center justify-between border-t px-4 py-3">
              <Button
                size="sm"
                variant="outline"
                aria-label={bt("bloem.admin.platformOrganizations.members.previousPageLabel")}
                disabled={membershipCursors.length === 1 || memberships.isFetching}
                onClick={() =>
                  setMembershipCursors((currentCursors) => currentCursors.slice(0, -1))
                }
              >
                <ChevronLeft className="h-4 w-4" />
                {bt("bloem.admin.platformOrganizations.members.previous")}
              </Button>
              <span className="text-muted-foreground text-xs">
                {bt("bloem.admin.platformOrganizations.members.page", {
                  page: membershipCursors.length,
                })}
              </span>
              <Button
                size="sm"
                variant="outline"
                aria-label={bt("bloem.admin.platformOrganizations.members.nextPageLabel")}
                disabled={!memberships.data?.next_cursor || memberships.isFetching}
                onClick={() =>
                  memberships.data?.next_cursor &&
                  setMembershipCursors((currentCursors) => [
                    ...currentCursors,
                    memberships.data!.next_cursor!,
                  ])
                }
              >
                {bt("bloem.admin.platformOrganizations.members.next")}
                <ChevronRight className="h-4 w-4" />
              </Button>
            </div>
          </CardContent>
        </Card>
        <Card className="h-fit">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <ShieldCheck className="h-4 w-4" />
              {bt("bloem.admin.platformOrganizations.detail.goodToKnow")}
            </CardTitle>
          </CardHeader>
          <CardContent className="text-muted-foreground space-y-3 text-sm">
            <p>{bt("bloem.admin.platformOrganizations.detail.activityLog")}</p>
            <p>{bt("bloem.admin.platformOrganizations.detail.suspending")}</p>
          </CardContent>
        </Card>
      </div>

      <OrganizationLifecyclePanel
        organization={current}
        activeMemberships={activeMemberships}
        onRevisionChanged={async () => {
          await organization.refetch();
        }}
      />
      <OrganizationEntitlementPanel organizationID={current.id} />
      <OrganizationDomainsPanel organizationID={current.id} />
      <OrganizationBrandingPanel organizationID={current.id} />
      <AddMembershipDialog
        organization={current}
        open={addMemberOpen}
        onOpenChange={setAddMemberOpen}
        onStale={async () => {
          await Promise.all([organization.refetch(), memberships.refetch()]);
        }}
      />
    </section>
  );
}
