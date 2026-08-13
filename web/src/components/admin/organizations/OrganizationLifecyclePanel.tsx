import { useState } from "react";
import { AdminV2ClientError } from "@/api/adminV2Client";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { OwnershipTransferDialog } from "./OwnershipTransferDialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  type PlatformOrganization,
  useSetOrganizationStatus,
  useTransferOrganizationOwnership,
  useUpdateOrganization,
} from "@/hooks/queries/admin/organizations";

interface OrganizationLifecyclePanelProps {
  organization: PlatformOrganization;
  activeMemberships: number;
  onRevisionChanged(message: string): Promise<void> | void;
}

function messageFrom(error: unknown): string {
  return error instanceof Error ? error.message : "The organization could not be updated.";
}

export function OrganizationLifecyclePanel({
  organization,
  activeMemberships,
  onRevisionChanged,
}: OrganizationLifecyclePanelProps) {
  const [name, setName] = useState(organization.name);
  const [slug, setSlug] = useState(organization.slug);
  const [confirmStatus, setConfirmStatus] = useState<"active" | "suspended" | null>(null);
  const [transferOpen, setTransferOpen] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const update = useUpdateOrganization(organization.id);
  const setStatus = useSetOrganizationStatus(organization.id);
  const transfer = useTransferOrganizationOwnership(organization.id);

  async function handleError(error: unknown) {
    if (error instanceof AdminV2ClientError && error.code === "authorization_state_changed") {
      const message = "Authorization state changed. Current organization data was reloaded.";
      setNotice(message);
      await onRevisionChanged(message);
      return;
    }
    if (error instanceof AdminV2ClientError && Object.keys(error.fields).length > 0) {
      setFieldErrors(error.fields);
    }
    setNotice(messageFrom(error));
  }

  async function saveIdentity() {
    setNotice(null);
    setFieldErrors({});
    try {
      await update.mutateAsync({
        expected_revision: organization.policy_revision,
        name: name.trim(),
        slug: slug.trim(),
      });
      setNotice("Organization details updated.");
    } catch (error) {
      await handleError(error);
    }
  }

  async function changeStatus() {
    if (!confirmStatus) return;
    setNotice(null);
    try {
      await setStatus.mutateAsync({
        expected_revision: organization.policy_revision,
        status: confirmStatus,
      });
      setNotice(
        confirmStatus === "suspended" ? "Organization suspended." : "Organization reactivated.",
      );
      setConfirmStatus(null);
    } catch (error) {
      setConfirmStatus(null);
      await handleError(error);
    }
  }

  return (
    <Card>
      <CardHeader className="border-b">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <CardTitle>Lifecycle and identity</CardTitle>
          <Badge variant={organization.status === "active" ? "default" : "secondary"}>
            {organization.status}
          </Badge>
        </div>
      </CardHeader>
      <CardContent className="space-y-5">
        {notice ? (
          <p className="border-border bg-muted/50 rounded-lg border p-3 text-sm" role="alert">
            {notice}
          </p>
        ) : null}
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor="organization-name">Name</Label>
            <Input id="organization-name" value={name} onChange={(e) => setName(e.target.value)} />
            {fieldErrors.name ? (
              <p className="text-destructive text-sm">{fieldErrors.name}</p>
            ) : null}
          </div>
          <div className="space-y-2">
            <Label htmlFor="organization-slug">Slug</Label>
            <Input id="organization-slug" value={slug} onChange={(e) => setSlug(e.target.value)} />
            {fieldErrors.slug ? (
              <p className="text-destructive text-sm">{fieldErrors.slug}</p>
            ) : null}
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            onClick={saveIdentity}
            disabled={
              update.isPending ||
              !name.trim() ||
              !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(slug) ||
              (name === organization.name && slug === organization.slug)
            }
          >
            {update.isPending ? "Saving…" : "Save details"}
          </Button>
          {organization.status === "suspended" ? (
            <Button variant="outline" onClick={() => setConfirmStatus("active")}>
              Reactivate organization
            </Button>
          ) : (
            <Button variant="destructive" onClick={() => setConfirmStatus("suspended")}>
              Suspend organization
            </Button>
          )}
          <Button variant="outline" onClick={() => setTransferOpen(true)}>
            Transfer ownership
          </Button>
        </div>
      </CardContent>

      <ConfirmDialog
        open={confirmStatus !== null}
        onOpenChange={(open) => !open && setConfirmStatus(null)}
        title={
          confirmStatus === "suspended"
            ? `Suspend ${organization.name}`
            : `Reactivate ${organization.name}`
        }
        description={
          confirmStatus === "suspended"
            ? `This blocks access for ${activeMemberships} active ${activeMemberships === 1 ? "membership" : "memberships"}. Media and audit history are retained.`
            : `This restores access for active memberships in ${organization.name}.`
        }
        confirmLabel={confirmStatus === "suspended" ? "Suspend" : "Reactivate"}
        variant={confirmStatus === "suspended" ? "destructive" : "default"}
        isPending={setStatus.isPending}
        onConfirm={() => void changeStatus()}
      />

      <OwnershipTransferDialog
        organization={organization}
        open={transferOpen}
        onOpenChange={(open) => {
          setTransferOpen(open);
          if (!open) transfer.reset();
        }}
        pending={transfer.isPending}
        error={transfer.error ? messageFrom(transfer.error) : null}
        onTransfer={({ ownerAccountId, password }) => {
          void transfer
            .mutateAsync({
              expected_revision: organization.policy_revision,
              owner_account_id: ownerAccountId,
              password,
            })
            .then(() => {
              setTransferOpen(false);
              setNotice("Ownership transferred.");
            })
            .catch((error: unknown) => void handleError(error));
        }}
      />
    </Card>
  );
}
