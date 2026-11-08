import { useState } from "react";
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
            <Label htmlFor="template-name">Name</Label>
            <Input
              id="template-name"
              placeholder="Standard Plus"
              value={name}
              onChange={(event) => {
                setName(event.target.value);
                if (!keyEdited) setKey(slugify(event.target.value));
              }}
            />
            <p className="text-muted-foreground text-xs">What admins and the storefront show.</p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="template-key">Key</Label>
            <Input
              id="template-key"
              className="font-mono"
              value={key}
              onChange={(event) => {
                setKey(event.target.value);
                setKeyEdited(true);
              }}
              disabled={Boolean(template)}
              placeholder="standard-plus"
            />
            <p className="text-muted-foreground text-xs">
              {template
                ? "A plan's key can't be changed."
                : "A short ID for links and the storefront. Lowercase, and it can't be changed later."}
            </p>
          </div>
          <PolicyCheckbox
            id="template-enabled"
            label="Offer this plan"
            description="Only offered plans can be given to organizations, accounts and storefront products. Accounts already on it are not affected."
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
            Browse-only lets people look around without playing anything, so playback and downloads
            stay off and can’t be switched on.
          </p>
        ) : null}
      </section>

      <section className="surface-panel space-y-4 rounded-2xl border-0 p-5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h2 className="text-sm font-medium">Libraries</h2>
            <p className="text-muted-foreground text-sm">
              A plan can only show libraries the organization or account already has.
            </p>
          </div>
        </div>
        <div className="grid gap-2 sm:grid-cols-2" role="radiogroup" aria-label="Library access">
          <label className="border-border has-[:checked]:border-primary has-[:checked]:bg-primary/5 flex cursor-pointer items-start gap-3 rounded-lg border p-3 text-sm transition-colors">
            <input
              type="radio"
              name="library-scope"
              className="mt-0.5"
              checked={policy.library_ids === null}
              onChange={() => updatePolicy({ library_ids: null })}
            />
            <span>
              <span className="block font-medium">All libraries</span>
              <span className="text-muted-foreground block">Including ones added later</span>
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
              <span className="block font-medium">Only the ones I pick</span>
              <span className="text-muted-foreground block">New libraries stay hidden</span>
            </span>
          </label>
        </div>
        {policy.library_ids === null ? null : libraries.length === 0 ? (
          <p className="text-muted-foreground text-sm">There are no libraries to pick from yet.</p>
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
        <h2 className="text-sm font-medium">Playback</h2>
        <PolicyCheckbox
          id="playback-allowed"
          label="Plays video"
          description="Turn off for a browse-only plan: people can look around but not play."
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
          <Label htmlFor="max-playback-quality">Best picture quality</Label>
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
            label="Screens at once"
            hint="0 means no limit"
            value={policy.max_streams}
            onChange={(max_streams) => updatePolicy({ max_streams })}
            disabled={!policy.playback_allowed}
          />
          <NumberField
            id="max-profiles"
            label="Profiles"
            hint="0 means no limit"
            value={policy.max_profiles}
            onChange={(max_profiles) => updatePolicy({ max_profiles })}
          />
          <NumberField
            id="max-transcodes"
            label="Conversions at once"
            hint="Videos converted for a device at the same time. 0 means no limit."
            value={policy.max_transcodes}
            onChange={(max_transcodes) => updatePolicy({ max_transcodes })}
            disabled={!policy.playback_allowed}
          />
        </div>
        <PolicyCheckbox
          id="transcode-allowed"
          label="Video conversion"
          description="Convert a video when the device can't play the file as it is. Off means only files the device can play directly will play."
          checked={policy.transcode_allowed}
          onChange={(transcode_allowed) => updatePolicy({ transcode_allowed })}
          disabled={browseOnly || !policy.playback_allowed}
        />
      </section>

      <section className="surface-panel space-y-3 rounded-2xl border-0 p-5">
        <h2 className="text-sm font-medium">Downloads and requests</h2>
        <PolicyCheckbox
          id="download-allowed"
          label="Download original files"
          description="Save the file as it is, for watching offline."
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
          label="Download converted copies"
          description="Save a smaller copy made for the device, where the app supports it."
          checked={policy.download_transcode_allowed}
          onChange={(download_transcode_allowed) => updatePolicy({ download_transcode_allowed })}
          disabled={browseOnly || !policy.playback_allowed || !policy.download_allowed}
        />
        <PolicyCheckbox
          id="requests-allowed"
          label="Media requests"
          description="Ask for movies and shows the server doesn't have yet."
          checked={policy.requests_allowed}
          onChange={(requests_allowed) => updatePolicy({ requests_allowed })}
        />
      </section>

      <section className="surface-panel space-y-3 rounded-2xl border-0 p-5">
        <div>
          <h2 className="text-sm font-medium">Extra permissions</h2>
          <p className="text-muted-foreground text-sm">
            Which extra permissions people on this plan can have, such as editing metadata.
          </p>
        </div>
        <PolicyCheckbox
          id="all-permissions"
          label="Allow every permission"
          description="Includes permissions added in future updates. Turn off to pick them one by one."
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
          {saving ? "Saving…" : template ? "Save as new revision" : "Create plan"}
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
