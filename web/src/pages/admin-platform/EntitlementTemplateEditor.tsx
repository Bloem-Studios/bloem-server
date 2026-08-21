import { useState } from "react";
import type {
  EntitlementTemplate,
  EntitlementTemplateInput,
  EntitlementTemplatePolicy,
} from "@/api/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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

const ENTITLEMENT_PERMISSIONS: ReadonlyArray<readonly [string, string]> = [
  [PERMISSION_METADATA_CURATION, "Metadata curation"],
  [PERMISSION_MARKER_EDIT, "Marker editing"],
];

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
  const [name, setName] = useState(template?.name ?? "New entitlement template");
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
            <Label htmlFor="template-key">Key</Label>
            <Input
              id="template-key"
              value={key}
              onChange={(event) => setKey(event.target.value)}
              disabled={Boolean(template)}
              placeholder="e.g. standard-plus"
            />
            <p className="text-muted-foreground text-xs">
              Stable lowercase identifier; it cannot change after creation.
            </p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="template-name">Name</Label>
            <Input
              id="template-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
          </div>
          <PolicyCheckbox
            id="template-enabled"
            label="Available for new mappings"
            description="Disabled templates stay readable but cannot be selected for new products."
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
            Browse-only does not permit playback. Its playback and download gates are protected so
            it remains safe to use for discovery-only access.
          </p>
        ) : null}
      </section>

      <section className="surface-panel space-y-4 rounded-2xl border-0 p-5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h2 className="text-sm font-semibold">Libraries</h2>
            <p className="text-muted-foreground text-xs">
              Choose the libraries members may browse.
            </p>
          </div>
          <div className="flex gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => updatePolicy({ library_ids: null })}
            >
              Select all libraries
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => updatePolicy({ library_ids: [] })}
            >
              Clear all libraries
            </Button>
          </div>
        </div>
        {libraries.length === 0 ? (
          <p className="text-muted-foreground text-sm">No libraries are available.</p>
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
        <h2 className="text-sm font-semibold">Playback and limits</h2>
        <PolicyCheckbox
          id="playback-allowed"
          label="Allow playback"
          description="Members can start playback from selected libraries."
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
          <Label htmlFor="max-playback-quality">Maximum playback quality</Label>
          <select
            id="max-playback-quality"
            className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm sm:w-72"
            value={policy.max_playback_quality}
            onChange={(event) => updatePolicy({ max_playback_quality: event.target.value })}
          >
            <option value="">Any quality</option>
            <option value="original">Original quality</option>
            <option value="1080p">Up to 1080p</option>
            <option value="2160p">Up to 4K</option>
          </select>
        </div>
        <div className="grid gap-4 sm:grid-cols-3">
          <NumberField
            id="max-streams"
            label="Max streams"
            hint="0 = no stream limit"
            value={policy.max_streams}
            onChange={(max_streams) => updatePolicy({ max_streams })}
            disabled={!policy.playback_allowed}
          />
          <NumberField
            id="max-profiles"
            label="Max profiles"
            hint="0 = no profile limit"
            value={policy.max_profiles}
            onChange={(max_profiles) => updatePolicy({ max_profiles })}
          />
          <NumberField
            id="max-transcodes"
            label="Max transcodes"
            hint="0 = no transcode limit"
            value={policy.max_transcodes}
            onChange={(max_transcodes) => updatePolicy({ max_transcodes })}
            disabled={!policy.playback_allowed}
          />
        </div>
        <PolicyCheckbox
          id="transcode-allowed"
          label="Allow playback transcoding"
          description="Members may play media that requires server-side conversion."
          checked={policy.transcode_allowed}
          onChange={(transcode_allowed) => updatePolicy({ transcode_allowed })}
          disabled={browseOnly || !policy.playback_allowed}
        />
      </section>

      <section className="surface-panel space-y-3 rounded-2xl border-0 p-5">
        <h2 className="text-sm font-semibold">Downloads and requests</h2>
        <PolicyCheckbox
          id="download-allowed"
          label="Allow downloads"
          description="Members may save original media files to their devices."
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
          label="Allow transcoded downloads"
          description="Members may download converted versions when supported."
          checked={policy.download_transcode_allowed}
          onChange={(download_transcode_allowed) => updatePolicy({ download_transcode_allowed })}
          disabled={browseOnly || !policy.playback_allowed || !policy.download_allowed}
        />
        <PolicyCheckbox
          id="requests-allowed"
          label="Allow media requests"
          description="Members may ask administrators to add unavailable media."
          checked={policy.requests_allowed}
          onChange={(requests_allowed) => updatePolicy({ requests_allowed })}
        />
      </section>

      <section className="surface-panel space-y-3 rounded-2xl border-0 p-5">
        <div>
          <h2 className="text-sm font-semibold">Permissions</h2>
          <p className="text-muted-foreground text-xs">
            Restrict which elevated media-management actions this entitlement may grant.
          </p>
        </div>
        <PolicyCheckbox
          id="all-permissions"
          label="Allow all permissions"
          description="New permissions are granted automatically. Turn this off to maintain an explicit allowlist."
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
            {ENTITLEMENT_PERMISSIONS.map(([permission, label]) => (
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
          {saving ? "Saving…" : "Save template"}
        </Button>
      </div>
    </form>
  );
}
