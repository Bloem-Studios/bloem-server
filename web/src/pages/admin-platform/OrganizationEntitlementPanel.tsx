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
import { PolicyValueChange } from "@/components/admin/policy/PolicyValueChange";
import { formatDateTime, libraryListLabel } from "@/components/admin/policy/policyVocabulary";
import { useAdminLibraries } from "@/hooks/queries/admin/libraries";

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
  return value ? formatDateTime(value) : "Not yet";
}

export function OrganizationEntitlementPanel({ organizationID }: { organizationID: string }) {
  const templates = useEntitlementTemplates(false);
  const detail = useOrganizationEntitlement(organizationID);
  const dryRun = useEntitlementDryRun(organizationID);
  const apply = useApplyTenantEntitlement(organizationID);
  const [selectedKey, setSelectedKey] = useState("");
  const [preview, setPreview] = useState<EntitlementDryRun | null>(null);
  const [confirming, setConfirming] = useState(false);
  const libraries = useAdminLibraries();
  const effectiveSelectedKey = selectedKey || detail.data?.template_key || "";
  const selected = templates.data?.find((template) => template.key === effectiveSelectedKey);
  const currentTemplateUnavailable =
    Boolean(detail.data?.template_key) &&
    !templates.data?.some((template) => template.key === detail.data?.template_key);

  async function previewChanges() {
    if (!selected) return;
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
    setPreview(null);
    dryRun.reset();
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Plan</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {detail.isLoading ? (
          <p className="text-sm" role="status">
            Loading…
          </p>
        ) : detail.isError ? (
          <p className="text-destructive text-sm" role="alert">
            The plan couldn’t be loaded.
          </p>
        ) : detail.data ? (
          <div className="border-border grid gap-3 rounded-lg border p-3 text-sm sm:grid-cols-2">
            <div>
              <p className="text-muted-foreground text-xs">Current plan</p>
              <p className="font-medium">
                {detail.data.template_key && detail.data.template_revision
                  ? `${detail.data.template_key}, revision ${detail.data.template_revision}`
                  : "No plan yet"}
              </p>
            </div>
            <div>
              <p className="text-muted-foreground text-xs">Organization limits</p>
              <p className="font-medium">
                {detail.data.tenant_limits.slots.toLocaleString()} screens ·{" "}
                {detail.data.tenant_limits.transcodes.toLocaleString()} conversions at once
              </p>
            </div>
            <div>
              <p className="text-muted-foreground text-xs">Libraries</p>
              <p className="font-medium">
                {libraryListLabel(detail.data.library_ids, libraries.data ?? [])}
              </p>
            </div>
            <div>
              <p className="text-muted-foreground text-xs">Last applied</p>
              <p className="font-medium">{formatTimestamp(detail.data.last_reconciled_at)}</p>
            </div>
            <div className="sm:col-span-2">
              <p className="text-muted-foreground text-xs">Default settings for members</p>
              {detail.data.managed_default_group ? (
                <div className="space-y-2">
                  <p className="font-medium">{detail.data.managed_default_group.name}</p>
                  <EntitlementPolicyDetails policy={detail.data.managed_default_group.policy} />
                </div>
              ) : (
                <p className="font-medium">None yet</p>
              )}
            </div>
            <Link
              className="underline sm:col-span-2"
              to={safeAuditHref(organizationID, detail.data.audit_history_href)}
            >
              See past changes
            </Link>
          </div>
        ) : null}
        <p className="text-muted-foreground text-sm">
          Choosing a plan changes the organization’s default settings for members. Access groups the
          organization made itself are never changed. You see the changes before they apply.
        </p>
        {templates.isLoading ? (
          <p className="text-sm" role="status">
            Loading plans…
          </p>
        ) : templates.isError ? (
          <p className="text-destructive text-sm" role="alert">
            Plans couldn’t be loaded.
          </p>
        ) : templates.data?.length === 0 ? (
          <div className="border-warning/40 bg-warning/10 space-y-1 rounded-lg border p-3 text-sm">
            <p>No plans are offered yet.</p>
            <Link className="underline" to="/admin/platform/entitlement-templates">
              Go to plans
            </Link>
          </div>
        ) : (
          <div className="space-y-1.5">
            <Label htmlFor="tenant-template">Plan</Label>
            <select
              id="tenant-template"
              className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
              value={effectiveSelectedKey}
              onChange={(event) => {
                setSelectedKey(event.target.value);
                setPreview(null);
                dryRun.reset();
                apply.reset();
              }}
            >
              <option value="">Choose a plan</option>
              {currentTemplateUnavailable ? (
                <option value={detail.data?.template_key ?? ""} disabled>
                  Current plan is no longer offered
                </option>
              ) : null}
              {(templates.data ?? []).map((template) => (
                <option key={template.key} value={template.key}>
                  {template.name} (revision {template.revision})
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
          {dryRun.isPending ? "Checking…" : "Preview changes"}
        </Button>
        {dryRun.error ? (
          <p className="text-destructive text-sm" role="alert">
            The preview couldn’t be made. Try again.
          </p>
        ) : null}
        {preview ? (
          <div className="border-border space-y-3 rounded-lg border p-3">
            <p className="text-sm font-medium">
              {preview.changed
                ? "These settings change"
                : "Nothing changes: it already uses this plan"}
            </p>
            {preview.changes.map((change) => (
              <PolicyValueChange key={change.field} change={change} />
            ))}
            {preview.warnings.map((warning) => (
              <p key={warning} className="text-warning text-sm">
                {warning}
              </p>
            ))}
            {preview.changed ? (
              <Button type="button" onClick={() => setConfirming(true)}>
                Apply
              </Button>
            ) : null}
          </div>
        ) : null}
        <AlertDialog open={confirming} onOpenChange={setConfirming}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>
                Put this organization on {selected?.name ?? "this plan"}?
              </AlertDialogTitle>
              <AlertDialogDescription>
                Members on the default settings get the changes you reviewed. Access groups the
                organization made itself stay as they are.
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
                Apply
              </AlertDialogAction>
            </AlertDialogFooter>
            {apply.error ? (
              <p className="text-destructive text-sm" role="alert">
                {apply.error.message || "The plan couldn’t be applied. Preview again and retry."}
              </p>
            ) : null}
          </AlertDialogContent>
        </AlertDialog>
      </CardContent>
    </Card>
  );
}
