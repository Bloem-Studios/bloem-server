import { useState } from "react";
import { useBloemT } from "@/bloem/i18n";
import type {
  EntitlementTemplate,
  EntitlementTemplateInput,
  EntitlementTemplatePolicy,
} from "@/api/bloemTypes";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PLAYBACK_QUALITIES, POLICY_PERMISSIONS } from "@/components/admin/policy/policyVocabulary";
import { PERMISSION_MARKER_EDIT, PERMISSION_METADATA_CURATION } from "@/lib/permissions";

export interface EntitlementTemplateLibrary {
  id: number;
  name: string;
}

interface EntitlementTemplateEditorProps {
  libraries: EntitlementTemplateLibrary[];
  template?: EntitlementTemplate;
  onSave(input: EntitlementTemplateInput): void | Promise<void>;
  saving?: boolean;
  error?: Error | null;
}

const DEFAULT_POLICY: EntitlementTemplatePolicy = {
  library_ids: null,
  playback_allowed: true,
  max_streams: 3,
  max_profiles: 5,
  transcode_allowed: true,
  max_transcodes: 1,
  download_allowed: true,
  download_transcode_allowed: true,
  max_playback_quality: "original",
  requests_allowed: true,
  allowed_permissions: null,
};

function policyFor(template?: EntitlementTemplate): EntitlementTemplatePolicy {
  return template
    ? { ...template.policy, allowed_permissions: template.policy.allowed_permissions ?? null }
    : DEFAULT_POLICY;
}

function selectedLibraryIDs(
  policy: EntitlementTemplatePolicy,
  libraries: EntitlementTemplateLibrary[],
) {
  return policy.library_ids ?? libraries.map((library) => library.id);
}

function NumberField({
  id,
  label,
  hint,
  value,
  onChange,
  disabled = false,
}: {
  id: string;
  label: string;
  hint: string;
  value: number;
  onChange(value: number): void;
  disabled?: boolean;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        type="number"
        min={0}
        value={value}
        disabled={disabled}
        onChange={(event) => {
          const next = Number.parseInt(event.target.value, 10);
          onChange(Number.isFinite(next) && next >= 0 ? next : 0);
        }}
      />
      <p className="text-muted-foreground text-xs">{hint}</p>
    </div>
  );
}

function PolicyCheckbox({
  id,
  label,
  description,
  checked,
  onChange,
  disabled = false,
}: {
  id: string;
  label: string;
  description: string;
  checked: boolean;
  onChange(checked: boolean): void;
  disabled?: boolean;
}) {
  return (
    <div className="border-border flex items-start gap-3 rounded-lg border px-3 py-2.5">
      <input
        id={id}
        type="checkbox"
        className="accent-primary mt-0.5 size-4"
        checked={checked}
        onChange={(event) => onChange(event.target.checked)}
        disabled={disabled}
      />
      <div className="min-w-0">
        <Label htmlFor={id} className="cursor-pointer text-sm font-medium">
          {label}
        </Label>
        <p className="text-muted-foreground mt-0.5 text-xs">{description}</p>
      </div>
    </div>
  );
}

export function EntitlementTemplateEditor(props: EntitlementTemplateEditorProps) {
  const identity = props.template
    ? `${props.template.key}:${props.template.revision}`
    : "new-template";
  return <EntitlementTemplateEditorForm key={identity} {...props} />;
}

function EntitlementTemplateEditorForm({
  libraries,
  template,
  onSave,
  saving = false,
  error,
}: EntitlementTemplateEditorProps) {
  const bt = useBloemT();
  const browseOnly = template?.key === "browse-only";
  const [key, setKey] = useState(template?.key ?? "");
  const [keyEdited, setKeyEdited] = useState(Boolean(template));
  const [name, setName] = useState(template?.name ?? "");
  const [policy, setPolicy] = useState<EntitlementTemplatePolicy>(policyFor(template));
  const [enabled, setEnabled] = useState(template?.enabled ?? true);

  const selected = selectedLibraryIDs(policy, libraries);
  function updatePolicy(patch: Partial<EntitlementTemplatePolicy>) {
    setPolicy((current) => ({ ...current, ...patch }));
  }

  function toggleLibrary(id: number, checked: boolean) {
    const current = selectedLibraryIDs(policy, libraries);
    const next = checked
      ? [...new Set([...current, id])]
      : current.filter((libraryID) => libraryID !== id);
    updatePolicy({
      library_ids: libraries
        .filter((library) => next.includes(library.id))
        .map((library) => library.id),
    });
  }

  async function save() {
    const playbackAllowed = !browseOnly && policy.playback_allowed;
    await onSave({
      key: template?.key ?? key.trim(),
      name: name.trim(),
      enabled,
      policy: {
        ...policy,
        playback_allowed: playbackAllowed,
        max_streams: playbackAllowed ? policy.max_streams : 0,
        transcode_allowed: playbackAllowed && policy.transcode_allowed,
        max_transcodes: playbackAllowed ? policy.max_transcodes : 0,
        download_allowed: playbackAllowed && policy.download_allowed,
        download_transcode_allowed:
          playbackAllowed && policy.download_allowed && policy.download_transcode_allowed,
      },
    });
  }

  return (
    <form
      className="space-y-5"
      onSubmit={(event) => {
        event.preventDefault();
        void save().catch(() => undefined);
      }}
    >
      <section className="surface-panel space-y-4 rounded-2xl border-0 p-5">
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label htmlFor="template-name">{bt("bloem.admin.plans.editor.name")}</Label>
            <Input
              id="template-name"
              placeholder={bt("bloem.admin.plans.editor.namePlaceholder")}
              value={name}
              onChange={(event) => {
                setName(event.target.value);
                if (!keyEdited) setKey(slugify(event.target.value));
              }}
            />
            <p className="text-muted-foreground text-xs">
              {bt("bloem.admin.plans.editor.nameHelp")}
            </p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="template-key">{bt("bloem.admin.plans.editor.key")}</Label>
            <Input
              id="template-key"
              className="font-mono"
              value={key}
              onChange={(event) => {
                setKey(event.target.value);
                setKeyEdited(true);
              }}
              disabled={Boolean(template)}
              placeholder={bt("bloem.admin.plans.editor.keyPlaceholder")}
            />
            <p className="text-muted-foreground text-xs">
              {template
                ? bt("bloem.admin.plans.editor.keyLocked")
                : bt("bloem.admin.plans.editor.keyHelp")}
            </p>
          </div>
          <PolicyCheckbox
            id="template-enabled"
            label={bt("bloem.admin.plans.editor.offer")}
            description={bt("bloem.admin.plans.editor.offerDescription")}
            checked={enabled}
            onChange={setEnabled}
          />
        </div>
        {error ? (
          <p className="text-destructive text-sm" role="alert">
            {error.message}
          </p>
        ) : null}
        {browseOnly ? (
          <p className="border-warning/40 bg-warning/10 rounded-lg border p-3 text-sm">
            {bt("bloem.admin.plans.editor.browseOnlyNotice")}
          </p>
        ) : null}
      </section>

      <section className="surface-panel space-y-4 rounded-2xl border-0 p-5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h2 className="text-sm font-medium">{bt("bloem.admin.plans.editor.libraries")}</h2>
            <p className="text-muted-foreground text-sm">
              {bt("bloem.admin.plans.editor.librariesHelp")}
            </p>
          </div>
        </div>
        <div
          className="grid gap-2 sm:grid-cols-2"
          role="radiogroup"
          aria-label={bt("bloem.admin.plans.editor.libraryAccess")}
        >
          <label className="border-border has-[:checked]:border-primary has-[:checked]:bg-primary/5 flex cursor-pointer items-start gap-3 rounded-lg border p-3 text-sm transition-colors">
            <input
              type="radio"
              name="library-scope"
              className="mt-0.5"
              checked={policy.library_ids === null}
              onChange={() => updatePolicy({ library_ids: null })}
            />
            <span>
              <span className="block font-medium">
                {bt("bloem.admin.plans.editor.allLibraries")}
              </span>
              <span className="text-muted-foreground block">
                {bt("bloem.admin.plans.editor.allLibrariesHelp")}
              </span>
            </span>
          </label>
          <label className="border-border has-[:checked]:border-primary has-[:checked]:bg-primary/5 flex cursor-pointer items-start gap-3 rounded-lg border p-3 text-sm transition-colors">
            <input
              type="radio"
              name="library-scope"
              className="mt-0.5"
              checked={policy.library_ids !== null}
              onChange={() => updatePolicy({ library_ids: libraries.map((library) => library.id) })}
            />
            <span>
              <span className="block font-medium">
                {bt("bloem.admin.plans.editor.pickedLibraries")}
              </span>
              <span className="text-muted-foreground block">
                {bt("bloem.admin.plans.editor.pickedLibrariesHelp")}
              </span>
            </span>
          </label>
        </div>
        {policy.library_ids === null ? null : libraries.length === 0 ? (
          <p className="text-muted-foreground text-sm">
            {bt("bloem.admin.plans.editor.noLibraries")}
          </p>
        ) : (
          <div className="grid gap-2 sm:grid-cols-2">
            {libraries.map((library) => (
              <label
                key={library.id}
                className="border-border flex cursor-pointer items-center gap-2 rounded-lg border px-3 py-2 text-sm"
              >
                <input
                  type="checkbox"
                  checked={selected.includes(library.id)}
                  onChange={(event) => toggleLibrary(library.id, event.target.checked)}
                />
                {library.name}
              </label>
            ))}
          </div>
        )}
      </section>

      <section className="surface-panel space-y-4 rounded-2xl border-0 p-5">
        <h2 className="text-sm font-medium">{bt("bloem.admin.plans.editor.playback")}</h2>
        <PolicyCheckbox
          id="playback-allowed"
          label={bt("bloem.admin.plans.editor.playsVideo")}
          description={bt("bloem.admin.plans.editor.playsVideoDescription")}
          checked={policy.playback_allowed}
          onChange={(playback_allowed) =>
            updatePolicy(
              playback_allowed
                ? { playback_allowed }
                : {
                    playback_allowed,
                    max_streams: 0,
                    transcode_allowed: false,
                    max_transcodes: 0,
                    download_allowed: false,
                    download_transcode_allowed: false,
                  },
            )
          }
          disabled={browseOnly}
        />
        <div className="space-y-1.5">
          <Label htmlFor="max-playback-quality">
            {bt("bloem.admin.plans.editor.bestPictureQuality")}
          </Label>
          <select
            id="max-playback-quality"
            className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm sm:w-72"
            value={policy.max_playback_quality}
            onChange={(event) => updatePolicy({ max_playback_quality: event.target.value })}
          >
            {PLAYBACK_QUALITIES.map(([value, label]) => (
              <option key={value || "any"} value={value}>
                {label}
              </option>
            ))}
          </select>
        </div>
        <div className="grid gap-4 sm:grid-cols-3">
          <NumberField
            id="max-streams"
            label={bt("bloem.admin.plans.editor.screensAtOnce")}
            hint={bt("bloem.admin.plans.editor.zeroMeansNoLimit")}
            value={policy.max_streams}
            onChange={(max_streams) => updatePolicy({ max_streams })}
            disabled={!policy.playback_allowed}
          />
          <NumberField
            id="max-profiles"
            label={bt("bloem.admin.plans.editor.profiles")}
            hint={bt("bloem.admin.plans.editor.zeroMeansNoLimit")}
            value={policy.max_profiles}
            onChange={(max_profiles) => updatePolicy({ max_profiles })}
          />
          <NumberField
            id="max-transcodes"
            label={bt("bloem.admin.plans.editor.conversionsAtOnce")}
            hint={bt("bloem.admin.plans.editor.conversionsAtOnceHelp")}
            value={policy.max_transcodes}
            onChange={(max_transcodes) => updatePolicy({ max_transcodes })}
            disabled={!policy.playback_allowed}
          />
        </div>
        <PolicyCheckbox
          id="transcode-allowed"
          label={bt("bloem.admin.plans.editor.videoConversion")}
          description={bt("bloem.admin.plans.editor.videoConversionDescription")}
          checked={policy.transcode_allowed}
          onChange={(transcode_allowed) => updatePolicy({ transcode_allowed })}
          disabled={browseOnly || !policy.playback_allowed}
        />
      </section>

      <section className="surface-panel space-y-3 rounded-2xl border-0 p-5">
        <h2 className="text-sm font-medium">
          {bt("bloem.admin.plans.editor.downloadsAndRequests")}
        </h2>
        <PolicyCheckbox
          id="download-allowed"
          label={bt("bloem.admin.plans.editor.downloadOriginals")}
          description={bt("bloem.admin.plans.editor.downloadOriginalsDescription")}
          checked={policy.download_allowed}
          onChange={(download_allowed) =>
            updatePolicy({
              download_allowed,
              download_transcode_allowed: download_allowed && policy.download_transcode_allowed,
            })
          }
          disabled={browseOnly || !policy.playback_allowed}
        />
        <PolicyCheckbox
          id="download-transcode-allowed"
          label={bt("bloem.admin.plans.editor.downloadConverted")}
          description={bt("bloem.admin.plans.editor.downloadConvertedDescription")}
          checked={policy.download_transcode_allowed}
          onChange={(download_transcode_allowed) => updatePolicy({ download_transcode_allowed })}
          disabled={browseOnly || !policy.playback_allowed || !policy.download_allowed}
        />
        <PolicyCheckbox
          id="requests-allowed"
          label={bt("bloem.admin.plans.editor.mediaRequests")}
          description={bt("bloem.admin.plans.editor.mediaRequestsDescription")}
          checked={policy.requests_allowed}
          onChange={(requests_allowed) => updatePolicy({ requests_allowed })}
        />
      </section>

      <section className="surface-panel space-y-3 rounded-2xl border-0 p-5">
        <div>
          <h2 className="text-sm font-medium">{bt("bloem.admin.plans.editor.extraPermissions")}</h2>
          <p className="text-muted-foreground text-sm">
            {bt("bloem.admin.plans.editor.extraPermissionsHelp")}
          </p>
        </div>
        <PolicyCheckbox
          id="all-permissions"
          label={bt("bloem.admin.plans.editor.allowEveryPermission")}
          description={bt("bloem.admin.plans.editor.allowEveryPermissionDescription")}
          checked={policy.allowed_permissions === null}
          onChange={(allowAll) =>
            updatePolicy({
              allowed_permissions: allowAll
                ? null
                : [PERMISSION_METADATA_CURATION, PERMISSION_MARKER_EDIT],
            })
          }
        />
        {policy.allowed_permissions !== null ? (
          <div className="grid gap-2 sm:grid-cols-2">
            {POLICY_PERMISSIONS.map(([permission, label]) => (
              <label
                key={permission}
                className="border-border flex cursor-pointer items-center gap-2 rounded-lg border px-3 py-2 text-sm"
              >
                <input
                  type="checkbox"
                  checked={policy.allowed_permissions?.includes(permission) ?? false}
                  onChange={(event) => {
                    const current = policy.allowed_permissions ?? [];
                    updatePolicy({
                      allowed_permissions: event.target.checked
                        ? [...new Set([...current, permission])].sort()
                        : current.filter((value) => value !== permission),
                    });
                  }}
                />
                {label}
              </label>
            ))}
          </div>
        ) : null}
      </section>

      <div className="flex justify-end">
        <Button type="submit" disabled={!key.trim() || !name.trim() || saving}>
          {saving
            ? bt("bloem.admin.plans.editor.saving")
            : template
              ? bt("bloem.admin.plans.editor.saveRevision")
              : bt("bloem.admin.plans.editor.create")}
        </Button>
      </div>
    </form>
  );
}

function slugify(value: string): string {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}
