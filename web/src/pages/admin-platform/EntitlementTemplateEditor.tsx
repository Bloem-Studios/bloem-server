import { useState } from "react";
import type {
  EntitlementTemplate,
  EntitlementTemplateInput,
  EntitlementTemplatePolicy,
} from "@/api/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

export interface EntitlementTemplateLibrary {
  id: number;
  name: string;
}

interface EntitlementTemplateEditorProps {
  libraries: EntitlementTemplateLibrary[];
  template?: EntitlementTemplate;
  onSave(input: EntitlementTemplateInput): void | Promise<void>;
  saving?: boolean;
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
};

function policyFor(template?: EntitlementTemplate): EntitlementTemplatePolicy {
  return template?.policy ?? DEFAULT_POLICY;
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
}: {
  id: string;
  label: string;
  hint: string;
  value: number;
  onChange(value: number): void;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        type="number"
        min={0}
        value={value}
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

export function EntitlementTemplateEditor({
  libraries,
  template,
  onSave,
  saving = false,
}: EntitlementTemplateEditorProps) {
  const browseOnly = template?.key === "browse-only";
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
    await onSave({
      key: template?.key,
      name: name.trim(),
      enabled,
      policy: {
        ...policy,
        playback_allowed: browseOnly ? false : policy.playback_allowed,
        download_allowed: browseOnly ? false : policy.download_allowed,
        download_transcode_allowed:
          !browseOnly && policy.download_allowed && policy.download_transcode_allowed,
      },
    });
  }

  return (
    <form
      className="space-y-5"
      onSubmit={(event) => {
        event.preventDefault();
        void save();
      }}
    >
      <section className="surface-panel space-y-4 rounded-2xl border-0 p-5">
        <div className="grid gap-4 sm:grid-cols-2">
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
          onChange={(playback_allowed) => updatePolicy({ playback_allowed })}
          disabled={browseOnly}
        />
        <div className="grid gap-4 sm:grid-cols-3">
          <NumberField
            id="max-streams"
            label="Max streams"
            hint="0 = no stream limit"
            value={policy.max_streams}
            onChange={(max_streams) => updatePolicy({ max_streams })}
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
          />
        </div>
        <PolicyCheckbox
          id="transcode-allowed"
          label="Allow playback transcoding"
          description="Members may play media that requires server-side conversion."
          checked={policy.transcode_allowed}
          onChange={(transcode_allowed) => updatePolicy({ transcode_allowed })}
          disabled={browseOnly}
        />
      </section>

      <section className="surface-panel space-y-3 rounded-2xl border-0 p-5">
        <h2 className="text-sm font-semibold">Downloads</h2>
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
          disabled={browseOnly}
        />
        <PolicyCheckbox
          id="download-transcode-allowed"
          label="Allow transcoded downloads"
          description="Members may download converted versions when supported."
          checked={policy.download_transcode_allowed}
          onChange={(download_transcode_allowed) => updatePolicy({ download_transcode_allowed })}
          disabled={browseOnly || !policy.download_allowed}
        />
      </section>

      <div className="flex justify-end">
        <Button type="submit" disabled={!name.trim() || saving}>
          {saving ? "Saving…" : "Save template"}
        </Button>
      </div>
    </form>
  );
}
