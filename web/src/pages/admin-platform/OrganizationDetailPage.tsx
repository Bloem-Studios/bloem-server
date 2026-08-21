import { useState } from "react";
import { AdminV2ClientError } from "@/api/adminV2Client";
import { ArrowLeft, ChevronLeft, ChevronRight, Plus, ShieldCheck, Users } from "lucide-react";
import { Link, useParams } from "react-router";
import { OrganizationLifecyclePanel } from "@/components/admin/organizations/OrganizationLifecyclePanel";
import { OrganizationEntitlementPanel } from "./OrganizationEntitlementPanel";
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
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

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
          <DialogTitle>Add membership</DialogTitle>
          <DialogDescription>
            Add an existing account to {organization.name}. Membership authority is scoped to this
            organization.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="membership-account">Account ID</Label>
            <Input
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
            <Label htmlFor="membership-role">Role</Label>
            <select
              id="membership-role"
              className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
              value={role}
              onChange={(e) => setRole(e.target.value as "admin" | "user")}
            >
              <option value="user">User</option>
              <option value="admin">Organization admin</option>
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
            Cancel
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
                      setError("Organization membership state changed. This page was reloaded.");
                      await onStale();
                      return;
                    }
                  }
                  setError(
                    cause instanceof Error ? cause.message : "Membership could not be added.",
                  );
                })
            }
          >
            {mutation.isPending ? "Adding…" : "Add membership"}
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
          setError("Membership changed. This page was reloaded.");
          await onStale();
          return;
        }
        setError(cause instanceof Error ? cause.message : "Membership update failed.");
      });
  }
  return (
    <TableRow>
      <TableCell>
        <span className="font-medium">
          {membership.username || `Account ${membership.account_id}`}
        </span>
        <span className="text-muted-foreground block text-xs">{membership.email}</span>
      </TableCell>
      <TableCell>
        {isOwner ? (
          <Badge variant="default">Owner</Badge>
        ) : (
          <Badge variant="outline">{membership.legacy_role === "admin" ? "Admin" : "User"}</Badge>
        )}
      </TableCell>
      <TableCell>
        <Badge variant={membership.status === "active" ? "secondary" : "outline"}>
          {membership.status}
        </Badge>
      </TableCell>
      <TableCell className="text-right">
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
            aria-label={`Suspend ${membership.username || `account ${membership.account_id}`}`}
            onClick={() => updateStatus("suspended")}
          >
            Suspend
          </Button>
        ) : (
          <Button
            size="sm"
            variant="outline"
            disabled={update.isPending}
            aria-label={`Reactivate ${membership.username || `account ${membership.account_id}`}`}
            onClick={() => updateStatus("active")}
          >
            Reactivate
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
  useDocumentTitle(organization.data?.name ?? "Organization");

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
        {organization.error?.message ?? "Organization not found."}
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
            <ArrowLeft className="mr-1 h-4 w-4" />
            Organizations
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
              {current.status}
            </Badge>
          </div>
          <p className="page-subtitle text-sm sm:text-base">
            {current.slug} · owner account {current.owner_account_id ?? "unassigned"} · policy
            revision {current.policy_revision}
          </p>
        </div>
      </div>

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
        <CountCard label="Memberships" value={current.membership_count ?? items.length} />
        <CountCard label="Profiles" value={current.profile_count ?? 0} />
        <CountCard label="Libraries" value={current.library_count ?? 0} />
        <CountCard label="Entitlements" value={current.entitlement_count ?? 0} />
        <CountCard label="Policy revision" value={current.policy_revision} />
      </div>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <Card>
          <CardHeader className="border-b">
            <div className="flex items-center justify-between gap-3">
              <CardTitle className="flex items-center gap-2">
                <Users className="h-4 w-4" />
                Memberships
              </CardTitle>
              <Button size="sm" onClick={() => setAddMemberOpen(true)}>
                <Plus className="mr-1 h-4 w-4" />
                Add
              </Button>
            </div>
          </CardHeader>
          <CardContent className="px-0">
            {memberships.isLoading ? (
              <div className="p-6 text-sm" role="status">
                Loading memberships…
              </div>
            ) : memberships.isError ? (
              <p className="text-destructive p-6 text-sm" role="alert">
                {memberships.error.message}
              </p>
            ) : items.length === 0 ? (
              <p className="text-muted-foreground p-6 text-sm">No memberships.</p>
            ) : (
              <div className="overflow-x-auto">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Account</TableHead>
                      <TableHead>Authority</TableHead>
                      <TableHead>Status</TableHead>
                      <TableHead>
                        <span className="sr-only">Actions</span>
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
                aria-label="Previous membership page"
                disabled={membershipCursors.length === 1 || memberships.isFetching}
                onClick={() =>
                  setMembershipCursors((currentCursors) => currentCursors.slice(0, -1))
                }
              >
                <ChevronLeft className="h-4 w-4" /> Previous
              </Button>
              <span className="text-muted-foreground text-xs">Page {membershipCursors.length}</span>
              <Button
                size="sm"
                variant="outline"
                aria-label="Next membership page"
                disabled={!memberships.data?.next_cursor || memberships.isFetching}
                onClick={() =>
                  memberships.data?.next_cursor &&
                  setMembershipCursors((currentCursors) => [
                    ...currentCursors,
                    memberships.data!.next_cursor!,
                  ])
                }
              >
                Next <ChevronRight className="h-4 w-4" />
              </Button>
            </div>
          </CardContent>
        </Card>
        <Card className="h-fit">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <ShieldCheck className="h-4 w-4" />
              Security state
            </CardTitle>
          </CardHeader>
          <CardContent className="text-muted-foreground space-y-3 text-sm">
            <p>
              Lifecycle and membership changes are revision guarded and written to the platform
              audit log.
            </p>
            <p>Suspension is reversible. This interface does not expose hard deletion.</p>
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
