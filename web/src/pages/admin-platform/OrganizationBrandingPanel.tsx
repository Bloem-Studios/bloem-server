import { useState } from "react";
import { useBloemT, type BloemTranslate } from "@/bloem/i18n";
import { Palette } from "lucide-react";
import { AdminV2ClientError } from "@/api/adminV2Client";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  ORGANIZATION_BRANDING_ASSETS,
  type OrganizationBranding,
  type OrganizationBrandingUpdate,
  useDeleteOrganizationBrandingAsset,
  useOrganizationBranding,
  useUpdateOrganizationBranding,
  useUploadOrganizationBrandingAsset,
} from "@/hooks/queries/admin/organizationDomains";

const FIELD_KEYS = ["server_name", "login_subtitle", "accent_color", "default_theme"] as const;

type FieldKey = (typeof FIELD_KEYS)[number];

/** Each field's label and example. The product name, hex code and theme are data. */
function fieldText(bt: BloemTranslate, key: FieldKey): { label: string; placeholder: string } {
  switch (key) {
    case "server_name":
      return {
        label: bt("bloem.admin.platformOrganizations.branding.serverName"),
        placeholder: "Bloem",
      };
    case "login_subtitle":
      return {
        label: bt("bloem.admin.platformOrganizations.branding.loginSubtitle"),
        placeholder: bt("bloem.admin.platformOrganizations.branding.loginSubtitlePlaceholder"),
      };
    case "accent_color":
      return {
        label: bt("bloem.admin.platformOrganizations.branding.accentColour"),
        placeholder: "#336699",
      };
    case "default_theme":
      return {
        label: bt("bloem.admin.platformOrganizations.branding.defaultTheme"),
        placeholder: "dark",
      };
  }
}

type AssetKind = (typeof ORGANIZATION_BRANDING_ASSETS)[number]["kind"];

function assetLabel(bt: BloemTranslate, kind: AssetKind): string {
  switch (kind) {
    case "wordmark":
      return bt("bloem.admin.platformOrganizations.branding.wordmark");
    case "mark":
      return bt("bloem.admin.platformOrganizations.branding.mark");
    case "favicon":
      return bt("bloem.admin.platformOrganizations.branding.favicon");
    case "login_bg":
      return bt("bloem.admin.platformOrganizations.branding.loginBackground");
  }
}

function valuesOf(branding: OrganizationBranding | undefined): Record<FieldKey, string> {
  return {
    server_name: branding?.server_name ?? "",
    login_subtitle: branding?.login_subtitle ?? "",
    accent_color: branding?.accent_color ?? "",
    default_theme: branding?.default_theme ?? "",
  };
}

/**
 * The text fields, starting from the loaded values. The parent keys it on
 * them, so a reload starts a fresh form instead of syncing state in an effect.
 */
function BrandingFields({
  loaded,
  saving,
  onSave,
}: {
  loaded: Record<FieldKey, string>;
  saving: boolean;
  onSave: (changed: OrganizationBrandingUpdate) => void;
}) {
  const bt = useBloemT();
  const [values, setValues] = useState(loaded);
  const fields = FIELD_KEYS.map((key) => ({ key, ...fieldText(bt, key) }));
  return (
    <form
      className="grid gap-3 sm:grid-cols-2"
      onSubmit={(event) => {
        event.preventDefault();
        const changed: OrganizationBrandingUpdate = {};
        for (const key of FIELD_KEYS) {
          if (values[key] !== loaded[key]) changed[key] = values[key];
        }
        if (Object.keys(changed).length > 0) onSave(changed);
      }}
    >
      {fields.map((field) => (
        <div key={field.key} className="grid gap-1">
          <Label htmlFor={`organization-branding-${field.key}`}>{field.label}</Label>
          <div className="flex gap-2">
            {field.key === "accent_color" ? (
              <input
                type="color"
                aria-label={bt("bloem.admin.platformOrganizations.branding.pickAccentColour")}
                className="border-input h-9 w-12 shrink-0 cursor-pointer rounded-md border bg-transparent p-1"
                value={
                  /^#[0-9a-f]{6}$/i.test(values.accent_color) ? values.accent_color : "#336699"
                }
                onChange={(event) =>
                  setValues((current) => ({ ...current, accent_color: event.target.value }))
                }
              />
            ) : null}
            <Input
              id={`organization-branding-${field.key}`}
              placeholder={field.placeholder}
              value={values[field.key]}
              onChange={(event) =>
                setValues((current) => ({ ...current, [field.key]: event.target.value }))
              }
            />
          </div>
        </div>
      ))}
      <div className="sm:col-span-2">
        <Button type="submit" disabled={saving}>
          {bt("bloem.admin.platformOrganizations.branding.save")}
        </Button>
      </div>
    </form>
  );
}

/**
 * An organization's own branding (Bloem), shown on its domains. Empty fields
 * use Bloem's built-in defaults there, never another organization's brand.
 */
export function OrganizationBrandingPanel({ organizationID }: { organizationID: string }) {
  const bt = useBloemT();
  const branding = useOrganizationBranding(organizationID);
  const update = useUpdateOrganizationBranding(organizationID);
  const upload = useUploadOrganizationBrandingAsset(organizationID);
  const removeAsset = useDeleteOrganizationBrandingAsset(organizationID);

  const defaultOrganization =
    branding.error instanceof AdminV2ClientError && branding.error.code === "default_organization";
  const error =
    update.error ??
    upload.error ??
    removeAsset.error ??
    (defaultOrganization ? null : branding.error);

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Palette className="h-4 w-4" />
          {bt("bloem.admin.platformOrganizations.branding.title")}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {defaultOrganization ? (
          <p className="text-muted-foreground text-sm">
            {bt("bloem.admin.platformOrganizations.branding.defaultOrganization")}
          </p>
        ) : branding.isLoading ? (
          <p className="text-sm" role="status">
            {bt("bloem.admin.platformOrganizations.branding.loading")}
          </p>
        ) : branding.data ? (
          <>
            <p className="text-muted-foreground text-sm">
              {bt("bloem.admin.platformOrganizations.branding.intro")}
            </p>
            <BrandingFields
              key={JSON.stringify(valuesOf(branding.data))}
              loaded={valuesOf(branding.data)}
              saving={update.isPending}
              onSave={(changed) => update.mutate(changed)}
            />
            <ul className="divide-border divide-y rounded-lg border">
              {ORGANIZATION_BRANDING_ASSETS.map((asset) => {
                const ref = branding.data?.assets?.[asset.kind];
                const inputID = `organization-branding-asset-${asset.kind}`;
                const label = assetLabel(bt, asset.kind);
                return (
                  <li key={asset.kind} className="flex flex-wrap items-center gap-3 p-3 text-sm">
                    <span className="font-medium">{label}</span>
                    <span className="text-muted-foreground text-xs">
                      {ref
                        ? bt("bloem.admin.platformOrganizations.branding.uploaded")
                        : bt("bloem.admin.platformOrganizations.branding.notSet")}
                    </span>
                    <span className="ms-auto flex items-center gap-2">
                      <label htmlFor={inputID} className="sr-only">
                        {bt("bloem.admin.platformOrganizations.branding.upload", { asset: label })}
                      </label>
                      <input
                        id={inputID}
                        type="file"
                        accept="image/png,image/jpeg,image/webp,image/x-icon,image/svg+xml"
                        className="text-xs"
                        disabled={upload.isPending}
                        onChange={(event) => {
                          const file = event.target.files?.[0];
                          if (file) upload.mutate({ kind: asset.kind, file });
                          event.target.value = "";
                        }}
                      />
                      {ref ? (
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={bt("bloem.admin.platformOrganizations.branding.removeAsset", {
                            asset: label,
                          })}
                          disabled={removeAsset.isPending}
                          onClick={() => removeAsset.mutate(asset.kind)}
                        >
                          {bt("bloem.admin.platformOrganizations.branding.remove")}
                        </Button>
                      ) : null}
                    </span>
                  </li>
                );
              })}
            </ul>
          </>
        ) : null}
        {error ? (
          <p className="text-destructive text-sm" role="alert">
            {error.message}
          </p>
        ) : null}
      </CardContent>
    </Card>
  );
}
