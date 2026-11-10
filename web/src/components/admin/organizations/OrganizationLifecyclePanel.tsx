import { bloemT, useBloemT } from "@/bloem/i18n";
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
  return error instanceof Error
    ? error.message
    : bloemT("bloem.admin.organizations.lifecycle.couldNotUpdate");
}

export function OrganizationLifecyclePanel({
  organization,
  activeMemberships,
  onRevisionChanged,
}: OrganizationLifecyclePanelProps) {
  const bt = useBloemT();
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
      const message = bt("bloem.admin.organizations.lifecycle.authorizationChanged");
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
      setNotice(bt("bloem.admin.organizations.lifecycle.detailsUpdated"));
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
        confirmStatus === "suspended"
          ? bt("bloem.admin.organizations.lifecycle.suspended")
          : bt("bloem.admin.organizations.lifecycle.reactivated"),
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
          <CardTitle>{bt("bloem.admin.organizations.lifecycle.nameAndStatus")}</CardTitle>
          <Badge
            variant={organization.status === "active" ? "default" : "secondary"}
            className="capitalize"
          >
            {bt("bloem.admin.organizations.lifecycle.status", { status: organization.status })}
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
            <Label htmlFor="organization-name">
              {bt("bloem.admin.organizations.lifecycle.name")}
            </Label>
            <Input id="organization-name" value={name} onChange={(e) => setName(e.target.value)} />
            {fieldErrors.name ? (
              <p className="text-destructive text-sm">{fieldErrors.name}</p>
            ) : null}
          </div>
          <div className="space-y-2">
            <Label htmlFor="organization-slug">
              {bt("bloem.admin.organizations.lifecycle.shortName")}
            </Label>
            <Input
              id="organization-slug"
              className="font-mono"
              value={slug}
              onChange={(e) => setSlug(e.target.value)}
            />
            <p className="text-muted-foreground text-xs">
              {bt("bloem.admin.organizations.lifecycle.usedInLinksChangingIt")}
            </p>
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
            {update.isPending
              ? bt("bloem.admin.organizations.lifecycle.saving")
              : bt("bloem.admin.organizations.lifecycle.saveDetails")}
          </Button>
          {organization.status === "suspended" ? (
            <Button variant="outline" onClick={() => setConfirmStatus("active")}>
              {bt("bloem.admin.organizations.lifecycle.reactivateOrganization")}
            </Button>
          ) : (
            <Button variant="destructive" onClick={() => setConfirmStatus("suspended")}>
              {bt("bloem.admin.organizations.lifecycle.suspendOrganization")}
            </Button>
          )}
          <Button variant="outline" onClick={() => setTransferOpen(true)}>
            {bt("bloem.admin.organizations.lifecycle.transferOwnership")}
          </Button>
        </div>
      </CardContent>

      <ConfirmDialog
        open={confirmStatus !== null}
        onOpenChange={(open) => !open && setConfirmStatus(null)}
        title={
          confirmStatus === "suspended"
            ? bt("bloem.admin.organizations.lifecycle.suspend", { name: organization.name })
            : bt("bloem.admin.organizations.lifecycle.reactivate", { name: organization.name })
        }
        description={
          confirmStatus === "suspended"
            ? bt("bloem.admin.organizations.lifecycle.suspendDescription", {
                count: activeMemberships,
              })
            : bt("bloem.admin.organizations.lifecycle.membersOfCanUseIt", {
                name: organization.name,
              })
        }
        confirmLabel={
          confirmStatus === "suspended"
            ? bt("bloem.admin.organizations.lifecycle.confirmSuspend")
            : bt("bloem.admin.organizations.lifecycle.confirmReactivate")
        }
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
        ownerError={
          transfer.error instanceof AdminV2ClientError
            ? transfer.error.fields.owner_account_id
            : null
        }
        onTransfer={({ ownerAccountId, password }) => {
          void transfer
            .mutateAsync({
              expected_revision: organization.policy_revision,
              owner_account_id: ownerAccountId,
              password,
            })
            .then(() => {
              setTransferOpen(false);
              setNotice(bt("bloem.admin.organizations.lifecycle.ownershipTransferred"));
            })
            .catch((error: unknown) => void handleError(error));
        }}
      />
    </Card>
  );
}
