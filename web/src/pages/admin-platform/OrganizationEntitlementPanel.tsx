import { useState } from "react";
import { Link } from "react-router";
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
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import {
  useApplyTenantEntitlement,
  useEntitlementDryRun,
  useEntitlementTemplates,
  useOrganizationEntitlement,
  type EntitlementDryRun,
} from "@/hooks/queries/admin/entitlementTemplates";
import { EntitlementPolicyDetails } from "./EntitlementPolicyDetails";

function safeAuditHref(organizationID: string, href: string | null) {
  const fallback = `/admin/activity?organization_id=${encodeURIComponent(organizationID)}&event=entitlement`;
  if (!href || href.startsWith("//")) return fallback;
  try {
    const url = new URL(href, window.location.origin);
    if (url.origin !== window.location.origin || url.pathname.startsWith("/api/")) return fallback;
    return `${url.pathname}${url.search}${url.hash}`;
  } catch {
    return fallback;
  }
}

function formatTimestamp(value: string | null) {
  if (!value) return "Never reconciled";
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(value));
}

export function OrganizationEntitlementPanel({ organizationID }: { organizationID: string }) {
  const templates = useEntitlementTemplates(false);
  const detail = useOrganizationEntitlement(organizationID);
  const dryRun = useEntitlementDryRun(organizationID);
  const apply = useApplyTenantEntitlement(organizationID);
  const [selectedKey, setSelectedKey] = useState("");
  const [preview, setPreview] = useState<EntitlementDryRun | null>(null);
  const [acknowledged, setAcknowledged] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const effectiveSelectedKey = selectedKey || detail.data?.template_key || "";
  const selected = templates.data?.find((template) => template.key === effectiveSelectedKey);
  const currentTemplateUnavailable =
    Boolean(detail.data?.template_key) &&
    !templates.data?.some((template) => template.key === detail.data?.template_key);

  async function previewChanges() {
    if (!selected) return;
    setAcknowledged(false);
    setPreview(
      await dryRun.mutateAsync({
        template_key: selected.key,
        template_revision: selected.revision,
      }),
    );
  }

  async function applyChanges() {
    if (!selected || !preview) return;
    await apply.mutateAsync({
      template_key: selected.key,
      template_revision: selected.revision,
      dry_run_token: preview.dry_run_token,
      idempotency_key: crypto.randomUUID(),
    });
    setConfirming(false);
    setAcknowledged(false);
    setPreview(null);
    dryRun.reset();
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Tenant entitlement</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {detail.isLoading ? (
          <p className="text-sm" role="status">
            Loading current entitlement…
          </p>
        ) : detail.isError ? (
          <p className="text-destructive text-sm" role="alert">
            {detail.error.message}
          </p>
        ) : detail.data ? (
          <div className="border-border grid gap-3 rounded-lg border p-3 text-sm sm:grid-cols-2">
            <div>
              <p className="text-muted-foreground text-xs">Current template</p>
              <p className="font-medium">
                {detail.data.template_key && detail.data.template_revision
                  ? `${detail.data.template_key} · revision ${detail.data.template_revision}`
                  : "No template assigned"}
              </p>
            </div>
            <div>
              <p className="text-muted-foreground text-xs">Tenant limits</p>
              <p className="font-medium">
                {detail.data.tenant_limits.slots} slots · {detail.data.tenant_limits.transcodes}{" "}
                transcodes
              </p>
            </div>
            <div>
              <p className="text-muted-foreground text-xs">Effective libraries</p>
              <p className="font-medium">
                {detail.data.library_ids === null
                  ? "All enabled libraries"
                  : detail.data.library_ids.length > 0
                    ? `Libraries ${detail.data.library_ids.join(", ")}`
                    : "No libraries"}
              </p>
            </div>
            <div>
              <p className="text-muted-foreground text-xs">Last reconciliation</p>
              <p className="font-medium">{formatTimestamp(detail.data.last_reconciled_at)}</p>
            </div>
            <div className="sm:col-span-2">
              <p className="text-muted-foreground text-xs">Managed default group</p>
              {detail.data.managed_default_group ? (
                <div className="space-y-2">
                  <p className="font-medium">
                    {detail.data.managed_default_group.name} ·{" "}
                    {detail.data.managed_default_group.policy.max_streams} streams ·{" "}
                    {detail.data.managed_default_group.policy.max_profiles} profiles ·{" "}
                    {detail.data.managed_default_group.policy.max_transcodes} transcodes
                  </p>
                  <EntitlementPolicyDetails policy={detail.data.managed_default_group.policy} />
                </div>
              ) : (
                <p className="font-medium">No managed default group</p>
              )}
            </div>
            <Link
              className="underline sm:col-span-2"
              to={safeAuditHref(organizationID, detail.data.audit_history_href)}
            >
              View apply history
            </Link>
          </div>
        ) : null}
        <p className="text-muted-foreground text-sm">
          Preview the managed default-group changes first. Custom groups are never modified.
        </p>
        {templates.isLoading ? (
          <p className="text-sm" role="status">
            Loading templates…
          </p>
        ) : templates.isError ? (
          <p className="text-destructive text-sm" role="alert">
            {templates.error.message}
          </p>
        ) : templates.data?.length === 0 ? (
          <div className="border-warning/40 bg-warning/10 space-y-1 rounded-lg border p-3 text-sm">
            <p>No enabled entitlement templates are available.</p>
            <Link className="underline" to="/admin/platform/entitlement-templates">
              Manage entitlement templates
            </Link>
          </div>
        ) : (
          <div className="space-y-1.5">
            <Label htmlFor="tenant-template">Template</Label>
            <select
              id="tenant-template"
              className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
              value={effectiveSelectedKey}
              onChange={(event) => {
                setSelectedKey(event.target.value);
                setPreview(null);
                setAcknowledged(false);
                dryRun.reset();
                apply.reset();
              }}
            >
              <option value="">Select a template</option>
              {currentTemplateUnavailable ? (
                <option value={detail.data?.template_key ?? ""} disabled>
                  Current template unavailable
                </option>
              ) : null}
              {(templates.data ?? []).map((template) => (
                <option key={template.key} value={template.key}>
                  {template.name} · rev {template.revision}
                </option>
              ))}
            </select>
          </div>
        )}
        <Button
          type="button"
          onClick={() => void previewChanges().catch(() => undefined)}
          disabled={!selected || dryRun.isPending}
        >
          Preview changes
        </Button>
        {dryRun.error ? (
          <p className="text-destructive text-sm" role="alert">
            {dryRun.error.message}
          </p>
        ) : null}
        {preview ? (
          <div className="border-border space-y-3 rounded-lg border p-3">
            <p className="text-sm font-medium">
              {preview.changed ? "Managed default group changes" : "No changes are required"}
            </p>
            {preview.changes.map((change) => (
              <p key={change.field} className="text-muted-foreground text-sm">
                <span className="text-foreground font-medium">{change.field}</span>:{" "}
                {String(change.before)} → {String(change.after)}
              </p>
            ))}
            {preview.warnings.map((warning) => (
              <p key={warning} className="text-warning text-sm">
                {warning}
              </p>
            ))}
            <label className="flex items-start gap-2 text-sm">
              <input
                type="checkbox"
                checked={acknowledged}
                onChange={(event) => setAcknowledged(event.target.checked)}
              />{" "}
              I understand this applies only to the managed default group.
            </label>
            <Button
              type="button"
              variant="destructive"
              disabled={!acknowledged || !preview.changed}
              onClick={() => setConfirming(true)}
            >
              Apply to existing tenant
            </Button>
          </div>
        ) : null}
        <AlertDialog open={confirming} onOpenChange={setConfirming}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Apply this entitlement?</AlertDialogTitle>
              <AlertDialogDescription>
                This updates the tenant-managed default group using the reviewed dry run. Custom
                groups stay unchanged.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction
                disabled={apply.isPending}
                onClick={(event) => {
                  event.preventDefault();
                  void applyChanges().catch(() => undefined);
                }}
              >
                Confirm apply
              </AlertDialogAction>
            </AlertDialogFooter>
            {apply.error ? (
              <p className="text-destructive text-sm" role="alert">
                {apply.error.message}
              </p>
            ) : null}
          </AlertDialogContent>
        </AlertDialog>
      </CardContent>
    </Card>
  );
}
