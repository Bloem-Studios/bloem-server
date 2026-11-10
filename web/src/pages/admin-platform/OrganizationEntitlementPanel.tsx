import { useState } from "react";
import { useBloemT } from "@/bloem/i18n";
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

export function OrganizationEntitlementPanel({ organizationID }: { organizationID: string }) {
  const bt = useBloemT();
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
        <CardTitle>{bt("bloem.admin.platformOrganizations.plan.title")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {detail.isLoading ? (
          <p className="text-sm" role="status">
            {bt("bloem.admin.platformOrganizations.plan.loading")}
          </p>
        ) : detail.isError ? (
          <p className="text-destructive text-sm" role="alert">
            {bt("bloem.admin.platformOrganizations.plan.loadFailed")}
          </p>
        ) : detail.data ? (
          <div className="border-border grid gap-3 rounded-lg border p-3 text-sm sm:grid-cols-2">
            <div>
              <p className="text-muted-foreground text-xs">
                {bt("bloem.admin.platformOrganizations.plan.current")}
              </p>
              <p className="font-medium">
                {detail.data.template_key && detail.data.template_revision
                  ? bt("bloem.admin.platformOrganizations.plan.currentValue", {
                      plan: detail.data.template_key,
                      revision: detail.data.template_revision,
                    })
                  : bt("bloem.admin.platformOrganizations.plan.none")}
              </p>
            </div>
            <div>
              <p className="text-muted-foreground text-xs">
                {bt("bloem.admin.platformOrganizations.plan.limits")}
              </p>
              <p className="font-medium">
                {bt("bloem.admin.platformOrganizations.plan.limitsValue", {
                  slots: detail.data.tenant_limits.slots,
                  transcodes: detail.data.tenant_limits.transcodes,
                })}
              </p>
            </div>
            <div>
              <p className="text-muted-foreground text-xs">
                {bt("bloem.admin.platformOrganizations.plan.libraries")}
              </p>
              <p className="font-medium">
                {libraryListLabel(detail.data.library_ids, libraries.data ?? [])}
              </p>
            </div>
            <div>
              <p className="text-muted-foreground text-xs">
                {bt("bloem.admin.platformOrganizations.plan.lastApplied")}
              </p>
              <p className="font-medium">
                {detail.data.last_reconciled_at
                  ? formatDateTime(detail.data.last_reconciled_at)
                  : bt("bloem.admin.platformOrganizations.plan.notYet")}
              </p>
            </div>
            <div className="sm:col-span-2">
              <p className="text-muted-foreground text-xs">
                {bt("bloem.admin.platformOrganizations.plan.memberDefaults")}
              </p>
              {detail.data.managed_default_group ? (
                <div className="space-y-2">
                  <p className="font-medium">{detail.data.managed_default_group.name}</p>
                  <EntitlementPolicyDetails policy={detail.data.managed_default_group.policy} />
                </div>
              ) : (
                <p className="font-medium">
                  {bt("bloem.admin.platformOrganizations.plan.noMemberDefaults")}
                </p>
              )}
            </div>
            <Link
              className="underline sm:col-span-2"
              to={safeAuditHref(organizationID, detail.data.audit_history_href)}
            >
              {bt("bloem.admin.platformOrganizations.plan.history")}
            </Link>
          </div>
        ) : null}
        <p className="text-muted-foreground text-sm">
          {bt("bloem.admin.platformOrganizations.plan.intro")}
        </p>
        {templates.isLoading ? (
          <p className="text-sm" role="status">
            {bt("bloem.admin.platformOrganizations.plan.loadingPlans")}
          </p>
        ) : templates.isError ? (
          <p className="text-destructive text-sm" role="alert">
            {bt("bloem.admin.platformOrganizations.plan.plansLoadFailed")}
          </p>
        ) : templates.data?.length === 0 ? (
          <div className="border-warning/40 bg-warning/10 space-y-1 rounded-lg border p-3 text-sm">
            <p>{bt("bloem.admin.platformOrganizations.plan.noPlans")}</p>
            <Link className="underline" to="/admin/platform/entitlement-templates">
              {bt("bloem.admin.platformOrganizations.plan.goToPlans")}
            </Link>
          </div>
        ) : (
          <div className="space-y-1.5">
            <Label htmlFor="tenant-template">
              {bt("bloem.admin.platformOrganizations.plan.label")}
            </Label>
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
              <option value="">{bt("bloem.admin.platformOrganizations.plan.choose")}</option>
              {currentTemplateUnavailable ? (
                <option value={detail.data?.template_key ?? ""} disabled>
                  {bt("bloem.admin.platformOrganizations.plan.currentUnavailable")}
                </option>
              ) : null}
              {(templates.data ?? []).map((template) => (
                <option key={template.key} value={template.key}>
                  {bt("bloem.admin.platformOrganizations.plan.option", {
                    name: template.name,
                    revision: template.revision,
                  })}
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
          {dryRun.isPending
            ? bt("bloem.admin.platformOrganizations.plan.checking")
            : bt("bloem.admin.platformOrganizations.plan.preview")}
        </Button>
        {dryRun.error ? (
          <p className="text-destructive text-sm" role="alert">
            {bt("bloem.admin.platformOrganizations.plan.previewFailed")}
          </p>
        ) : null}
        {preview ? (
          <div className="border-border space-y-3 rounded-lg border p-3">
            <p className="text-sm font-medium">
              {preview.changed
                ? bt("bloem.admin.platformOrganizations.plan.changes")
                : bt("bloem.admin.platformOrganizations.plan.noChanges")}
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
                {bt("bloem.admin.platformOrganizations.plan.apply")}
              </Button>
            ) : null}
          </div>
        ) : null}
        <AlertDialog open={confirming} onOpenChange={setConfirming}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>
                {selected
                  ? bt("bloem.admin.platformOrganizations.plan.confirmTitle", {
                      plan: selected.name,
                    })
                  : bt("bloem.admin.platformOrganizations.plan.confirmTitleUnnamed")}
              </AlertDialogTitle>
              <AlertDialogDescription>
                {bt("bloem.admin.platformOrganizations.plan.confirmDescription")}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>
                {bt("bloem.admin.platformOrganizations.plan.cancel")}
              </AlertDialogCancel>
              <AlertDialogAction
                disabled={apply.isPending}
                onClick={(event) => {
                  event.preventDefault();
                  void applyChanges().catch(() => undefined);
                }}
              >
                {bt("bloem.admin.platformOrganizations.plan.confirmApply")}
              </AlertDialogAction>
            </AlertDialogFooter>
            {apply.error ? (
              <p className="text-destructive text-sm" role="alert">
                {apply.error.message || bt("bloem.admin.platformOrganizations.plan.applyFailed")}
              </p>
            ) : null}
          </AlertDialogContent>
        </AlertDialog>
      </CardContent>
    </Card>
  );
}
