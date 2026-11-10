import { ArrowLeft, Plus, Trash2, UsersRound } from "lucide-react";
import { useMemo, useState } from "react";

import type { AccessGroup, AccessGroupInput } from "@/api/types";
import { bloemT, useBloemT } from "@/bloem/i18n";
import { LibraryAccessScope } from "@/bloem/libraryAccessScope";
import { LibraryAccessSelector } from "@/components/LibraryAccessSelector";
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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import {
  useAccessGroups,
  useCreateAccessGroup,
  useDeleteAccessGroup,
  useUpdateAccessGroup,
} from "@/hooks/queries/admin/organizationAccessGroups";
import { useAdminLibraries } from "@/hooks/queries/admin/libraries";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import { useOrganizationLibraries } from "@/hooks/queries/admin/organizationLibraries";
import { PERMISSION_WATCH_LIVE_TV } from "@/lib/bloemPermissions";
import { PERMISSION_MARKER_EDIT, PERMISSION_METADATA_CURATION } from "@/lib/permissions";
import {
  PLAYBACK_QUALITY_OPTIONS,
  playbackQualityPresetFromValue,
  playbackQualityValueFromPreset,
  type PlaybackQualityPreset,
} from "@/lib/playback-quality";

// The two assignable permissions (mirrors auth.assignablePermissions). A group
// mask of `null` means "all assignable"; a list narrows to those named.
const ASSIGNABLE_PERMISSIONS: Array<{ value: string; label: string; description: string }> = [
  {
    value: PERMISSION_WATCH_LIVE_TV,
    get label() {
      return bloemT("bloem.admin.orgAccessGroups.permissions.liveTvLabel");
    },
    get description() {
      return bloemT("bloem.admin.orgAccessGroups.permissions.liveTvDescription");
    },
  },
  {
    value: PERMISSION_METADATA_CURATION,
    get label() {
      return bloemT("bloem.admin.orgAccessGroups.permissions.metadataLabel");
    },
    get description() {
      return bloemT("bloem.admin.orgAccessGroups.permissions.metadataDescription");
    },
  },
  {
    value: PERMISSION_MARKER_EDIT,
    get label() {
      return bloemT("bloem.admin.orgAccessGroups.permissions.markersLabel");
    },
    get description() {
      return bloemT("bloem.admin.orgAccessGroups.permissions.markersDescription");
    },
  },
];

export default function OrganizationAdminAccessGroups() {
  const bt = useBloemT();
  useDocumentTitle(bt("bloem.admin.orgAccessGroups.title"));
  const { active } = useAdminContext();
  const groups = useAccessGroups(active);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");
  const createGroup = useCreateAccessGroup(active);

  const selected = useMemo(
    () => groups.data?.find((group) => group.id === selectedId),
    [groups.data, selectedId],
  );

  async function create() {
    const name = newName.trim();
    if (!name) return;
    const group = await createGroup.mutateAsync({ name });
    setNewName("");
    setCreating(false);
    setSelectedId(group.id);
  }

  if (selected) {
    return (
      <div className="page-shell space-y-6 py-4 sm:py-6">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="text-muted-foreground -ms-2 w-fit"
          onClick={() => setSelectedId(null)}
        >
          <ArrowLeft className="size-4" />
          {bt("bloem.admin.orgAccessGroups.allGroups")}
        </Button>
        <AccessGroupEditor
          key={selected.id}
          group={selected}
          defaultGroupName={
            groups.data?.find((group) => group.is_default)?.name ??
            bt("bloem.admin.orgAccessGroups.theDefaultGroup")
          }
          onDeleted={() => setSelectedId(null)}
        />
      </div>
    );
  }

  return (
    <div className="page-shell space-y-6 py-4 sm:py-6">
      <div className="page-header gap-5">
        <div className="space-y-3">
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]">
            {bt("bloem.admin.orgAccessGroups.title")}
          </h1>
          <p className="page-subtitle text-sm sm:text-base">
            {active?.scope === "organization"
              ? bt("bloem.admin.orgAccessGroups.introForOrganization", { name: active.name })
              : bt("bloem.admin.orgAccessGroups.intro")}
          </p>
        </div>
        {!creating && (
          <Button type="button" onClick={() => setCreating(true)}>
            <Plus className="size-4" />
            {bt("bloem.admin.orgAccessGroups.newGroup")}
          </Button>
        )}
      </div>

      {creating && (
        <div className="surface-panel-subtle flex flex-wrap items-center gap-2 rounded-2xl p-4">
          <Input
            value={newName}
            onChange={(event) => setNewName(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") void create();
            }}
            placeholder={bt("bloem.admin.orgAccessGroups.newGroupPlaceholder")}
            aria-label={bt("bloem.admin.orgAccessGroups.newGroupName")}
            className="max-w-xs"
            autoFocus
          />
          <Button type="button" onClick={create} disabled={createGroup.isPending}>
            {bt("bloem.admin.orgAccessGroups.create")}
          </Button>
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              setCreating(false);
              setNewName("");
            }}
          >
            {bt("bloem.admin.orgAccessGroups.cancel")}
          </Button>
        </div>
      )}

      {groups.isLoading && (
        <p className="text-muted-foreground text-sm">{bt("bloem.admin.orgAccessGroups.loading")}</p>
      )}

      {!groups.isLoading && groups.data?.length === 0 && !creating && (
        <div className="surface-panel-subtle rounded-2xl p-8 text-center">
          <UsersRound className="text-muted-foreground mx-auto size-8" />
          <h2 className="mt-3 text-lg font-semibold">
            {bt("bloem.admin.orgAccessGroups.emptyTitle")}
          </h2>
          <p className="text-muted-foreground mx-auto mt-1 max-w-md text-sm">
            {bt("bloem.admin.orgAccessGroups.emptyDescription")}
          </p>
        </div>
      )}

      {groups.data && groups.data.length > 0 && (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {groups.data.map((group) => (
            <AccessGroupCard key={group.id} group={group} onClick={() => setSelectedId(group.id)} />
          ))}
        </div>
      )}
    </div>
  );
}

function AccessGroupCard({ group, onClick }: { group: AccessGroup; onClick: () => void }) {
  const bt = useBloemT();
  const facts = [
    group.library_ids === null
      ? bt("bloem.admin.orgAccessGroups.card.allLibraries")
      : bt("bloem.admin.orgAccessGroups.card.libraryCount", { count: group.library_ids.length }),
    group.download_allowed
      ? bt("bloem.admin.orgAccessGroups.card.downloadsOn")
      : bt("bloem.admin.orgAccessGroups.card.noDownloads"),
    group.max_streams > 0
      ? bt("bloem.admin.orgAccessGroups.card.streamCount", { count: group.max_streams })
      : bt("bloem.admin.orgAccessGroups.card.unlimitedStreams"),
    group.requests_allowed
      ? bt("bloem.admin.orgAccessGroups.card.requestsOn")
      : bt("bloem.admin.orgAccessGroups.card.noRequests"),
  ];
  return (
    <button
      type="button"
      onClick={onClick}
      className="surface-panel hover:border-ring/40 focus-visible:ring-ring/60 flex flex-col gap-3 rounded-2xl border border-transparent p-5 text-start transition-colors outline-none focus-visible:ring-2"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h2 className="truncate text-base font-semibold tracking-tight">{group.name}</h2>
            {group.is_default && (
              <span className="border-border text-muted-foreground shrink-0 rounded-full border px-2 py-0.5 text-[10px] font-medium tracking-wide uppercase">
                {bt("bloem.admin.orgAccessGroups.card.defaultBadge")}
              </span>
            )}
          </div>
          {group.description && (
            <p className="text-muted-foreground mt-0.5 line-clamp-2 text-sm">{group.description}</p>
          )}
        </div>
        <span className="bg-secondary text-secondary-foreground shrink-0 rounded-full px-2.5 py-0.5 text-xs font-medium">
          {bt("bloem.admin.orgAccessGroups.card.memberCount", { count: group.member_count })}
        </span>
      </div>
      <div className="flex flex-wrap gap-1.5">
        {facts.map((fact) => (
          <span
            key={fact}
            className="border-border text-muted-foreground rounded-md border px-2 py-0.5 text-xs"
          >
            {fact}
          </span>
        ))}
      </div>
    </button>
  );
}

interface AccessGroupEditorProps {
  group: AccessGroup;
  defaultGroupName: string;
  onDeleted: () => void;
}

function AccessGroupEditor({ group, defaultGroupName, onDeleted }: AccessGroupEditorProps) {
  const bt = useBloemT();
  const { active } = useAdminContext();
  const organization = active?.scope === "organization";
  const legacyLibraries = useAdminLibraries({ enabled: !organization });
  const organizationLibraries = useOrganizationLibraries(
    active?.key ?? "organization:unavailable",
    organization,
  );
  const libraries = organization
    ? (organizationLibraries.data ?? []).map((library) => ({
        id: library.folder_id,
        name: library.name,
        type: library.type,
        enabled: library.access_kind === "owned" || library.entitlement?.status === "active",
      }))
    : (legacyLibraries.data ?? []);
  const updateGroup = useUpdateAccessGroup(active);
  const deleteGroup = useDeleteAccessGroup(active);
  const [confirmDelete, setConfirmDelete] = useState(false);

  // Draft state, keyed by group id via the parent's selection so switching
  // groups remounts this component with fresh initial values.
  const [name, setName] = useState(group.name);
  const [description, setDescription] = useState(group.description);
  const [libraryIds, setLibraryIds] = useState<number[] | null>(group.library_ids);
  const [qualityPreset, setQualityPreset] = useState<PlaybackQualityPreset>(
    playbackQualityPresetFromValue(group.max_playback_quality),
  );
  const [downloadAllowed, setDownloadAllowed] = useState(group.download_allowed);
  const [transcodeAllowed, setTranscodeAllowed] = useState(group.download_transcode_allowed);
  const [videoTranscodeAllowed, setVideoTranscodeAllowed] = useState(group.transcode_allowed);
  const [audioTranscodeAllowed, setAudioTranscodeAllowed] = useState(group.audio_transcode_allowed);
  const [maxStreams, setMaxStreams] = useState(group.max_streams);
  const [maxTranscodes, setMaxTranscodes] = useState(group.max_transcodes);
  const [permissions, setPermissions] = useState<string[] | null>(group.allowed_permissions);
  const [requestsAllowed, setRequestsAllowed] = useState(group.requests_allowed);
  const [isDefault, setIsDefault] = useState(group.is_default);

  const allPermissions = permissions === null;

  function setPermissionAllowed(permission: string, allowed: boolean) {
    const current = permissions ?? ASSIGNABLE_PERMISSIONS.map((entry) => entry.value);
    const next = allowed
      ? Array.from(new Set([...current, permission]))
      : current.filter((value) => value !== permission);
    setPermissions(next);
  }

  async function save() {
    const body: AccessGroupInput = {
      name: name.trim(),
      description: description.trim(),
      library_ids: libraryIds,
      max_playback_quality: playbackQualityValueFromPreset(qualityPreset),
      download_allowed: downloadAllowed,
      // The transcode toggle is disabled (not reset) when downloads are off,
      // so clamp it here to avoid saving a contradictory record.
      download_transcode_allowed: downloadAllowed && transcodeAllowed,
      transcode_allowed: videoTranscodeAllowed,
      audio_transcode_allowed: audioTranscodeAllowed,
      max_streams: maxStreams,
      max_transcodes: maxTranscodes,
      allowed_permissions: permissions,
      requests_allowed: requestsAllowed,
      is_default: isDefault,
    };
    await updateGroup.mutateAsync({ id: group.id, body });
  }

  async function remove() {
    await deleteGroup.mutateAsync(group.id);
    setConfirmDelete(false);
    onDeleted();
  }

  return (
    <div className="space-y-5">
      <div className="surface-panel space-y-4 rounded-2xl border-0 p-5">
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor="group-name">{bt("bloem.admin.orgAccessGroups.editor.name")}</Label>
            <Input id="group-name" value={name} onChange={(event) => setName(event.target.value)} />
          </div>
          <div className="space-y-2">
            <Label htmlFor="group-description">
              {bt("bloem.admin.orgAccessGroups.editor.description")}
            </Label>
            <Input
              id="group-description"
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              placeholder={bt("bloem.admin.orgAccessGroups.editor.optional")}
            />
          </div>
        </div>
        <ToggleRow
          label={bt("bloem.admin.orgAccessGroups.editor.defaultForNewUsers")}
          description={
            group.is_default
              ? bt("bloem.admin.orgAccessGroups.editor.defaultForNewUsersIsDefault")
              : bt("bloem.admin.orgAccessGroups.editor.defaultForNewUsersDescription")
          }
          checked={isDefault}
          onCheckedChange={setIsDefault}
          disabled={group.is_default}
        />
      </div>

      <section className="surface-panel space-y-4 rounded-2xl border-0 p-5">
        <h2 className="text-sm font-semibold">
          {bt("bloem.admin.orgAccessGroups.editor.librariesAndPlayback")}
        </h2>
        <LibraryAccessScope name={organization ? active?.name : undefined}>
          <LibraryAccessSelector
            libraries={libraries}
            value={libraryIds}
            onChange={setLibraryIds}
          />
        </LibraryAccessScope>
        <div className="space-y-2">
          <Label htmlFor="group-quality">
            {bt("bloem.admin.orgAccessGroups.editor.bestPictureQuality")}
          </Label>
          <Select
            value={qualityPreset}
            onValueChange={(value) => setQualityPreset(value as PlaybackQualityPreset)}
          >
            <SelectTrigger id="group-quality" className="w-full sm:w-72">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {PLAYBACK_QUALITY_OPTIONS.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {bt("bloem.admin.orgAccessGroups.editor.qualityOption", {
                    label: option.label,
                    description: option.description,
                  })}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </section>

      <section className="surface-panel space-y-3 rounded-2xl border-0 p-5">
        <h2 className="text-sm font-semibold">
          {bt("bloem.admin.orgAccessGroups.editor.downloadsAndRequests")}
        </h2>
        <ToggleRow
          label={bt("bloem.admin.orgAccessGroups.editor.downloadOriginals")}
          description={bt("bloem.admin.orgAccessGroups.editor.downloadOriginalsDescription")}
          checked={downloadAllowed}
          onCheckedChange={setDownloadAllowed}
        />
        <ToggleRow
          label={bt("bloem.admin.orgAccessGroups.editor.downloadConverted")}
          description={bt("bloem.admin.orgAccessGroups.editor.downloadConvertedDescription")}
          checked={transcodeAllowed}
          onCheckedChange={setTranscodeAllowed}
          disabled={!downloadAllowed}
        />
        <ToggleRow
          label={bt("bloem.admin.orgAccessGroups.editor.mediaRequests")}
          description={bt("bloem.admin.orgAccessGroups.editor.mediaRequestsDescription")}
          checked={requestsAllowed}
          onCheckedChange={setRequestsAllowed}
        />
      </section>

      <section className="surface-panel space-y-4 rounded-2xl border-0 p-5">
        <h2 className="text-sm font-semibold">
          {bt("bloem.admin.orgAccessGroups.editor.concurrentStreams")}
        </h2>
        <div className="grid gap-4 sm:grid-cols-2">
          <LimitField
            id="group-streams"
            label={bt("bloem.admin.orgAccessGroups.editor.screensAtOnce")}
            hint={bt("bloem.admin.orgAccessGroups.editor.zeroIsUnlimited")}
            value={maxStreams}
            onChange={setMaxStreams}
          />
          <LimitField
            id="group-transcodes"
            label={bt("bloem.admin.orgAccessGroups.editor.conversionsAtOnce")}
            hint={bt("bloem.admin.orgAccessGroups.editor.zeroIsUnlimited")}
            value={maxTranscodes}
            onChange={setMaxTranscodes}
          />
        </div>
        <ToggleRow
          label={bt("bloem.admin.orgAccessGroups.editor.videoConversion")}
          description={bt("bloem.admin.orgAccessGroups.editor.videoConversionDescription")}
          checked={videoTranscodeAllowed}
          onCheckedChange={setVideoTranscodeAllowed}
        />
        <ToggleRow
          label={bt("bloem.admin.orgAccessGroups.editor.audioConversion")}
          description={bt("bloem.admin.orgAccessGroups.editor.audioConversionDescription")}
          checked={audioTranscodeAllowed}
          onCheckedChange={setAudioTranscodeAllowed}
        />
      </section>

      <section className="surface-panel space-y-3 rounded-2xl border-0 p-5">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-sm font-semibold">
              {bt("bloem.admin.orgAccessGroups.editor.permissions")}
            </h2>
            <p className="text-muted-foreground mt-0.5 text-xs">
              {bt("bloem.admin.orgAccessGroups.editor.permissionsHelp")}
            </p>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-muted-foreground text-xs">
              {bt("bloem.admin.orgAccessGroups.editor.allowAll")}
            </span>
            <Switch
              checked={allPermissions}
              onCheckedChange={(checked) =>
                setPermissions(checked ? null : ASSIGNABLE_PERMISSIONS.map((entry) => entry.value))
              }
              aria-label={bt("bloem.admin.orgAccessGroups.editor.allowEveryPermission")}
            />
          </div>
        </div>
        {!allPermissions && (
          <div className="space-y-2">
            {ASSIGNABLE_PERMISSIONS.map((permission) => (
              <ToggleRow
                key={permission.value}
                label={permission.label}
                description={permission.description}
                checked={permissions?.includes(permission.value) ?? false}
                onCheckedChange={(checked) => setPermissionAllowed(permission.value, checked)}
              />
            ))}
          </div>
        )}
      </section>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-col gap-1">
          <Button
            type="button"
            variant="ghost"
            className="text-destructive hover:text-destructive"
            onClick={() => setConfirmDelete(true)}
            disabled={group.is_default}
          >
            <Trash2 className="size-4" />
            {bt("bloem.admin.orgAccessGroups.editor.deleteGroup")}
          </Button>
          {group.is_default && (
            <p className="text-muted-foreground text-xs">
              {bt("bloem.admin.orgAccessGroups.editor.defaultCannotBeDeleted")}
            </p>
          )}
        </div>
        <Button type="button" onClick={save} disabled={updateGroup.isPending}>
          {updateGroup.isPending
            ? bt("bloem.admin.orgAccessGroups.editor.saving")
            : bt("bloem.admin.orgAccessGroups.editor.saveChanges")}
        </Button>
      </div>

      <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {bt("bloem.admin.orgAccessGroups.delete.title", { name: group.name })}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {organization
                ? group.member_count > 0
                  ? bt("bloem.admin.orgAccessGroups.delete.reassignProfiles", {
                      count: group.member_count,
                      organization:
                        active?.name ?? bt("bloem.admin.orgAccessGroups.delete.thisOrganization"),
                      defaultGroup: defaultGroupName,
                    })
                  : bt("bloem.admin.orgAccessGroups.delete.noProfiles", {
                      organization:
                        active?.name ?? bt("bloem.admin.orgAccessGroups.delete.thisOrganization"),
                    })
                : group.member_count > 0
                  ? bt("bloem.admin.orgAccessGroups.delete.membersMoveToNoGroup", {
                      count: group.member_count,
                    })
                  : bt("bloem.admin.orgAccessGroups.delete.noMembers")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{bt("bloem.admin.orgAccessGroups.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={remove}
              disabled={deleteGroup.isPending}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {bt("bloem.admin.orgAccessGroups.delete.confirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

interface ToggleRowProps {
  label: string;
  description: string;
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  disabled?: boolean;
}

function ToggleRow({ label, description, checked, onCheckedChange, disabled }: ToggleRowProps) {
  return (
    <div className="border-border flex items-center justify-between gap-4 rounded-lg border px-3 py-2.5">
      <div className="min-w-0">
        <p className="text-sm font-medium">{label}</p>
        <p className="text-muted-foreground text-xs">{description}</p>
      </div>
      <Switch
        checked={checked}
        onCheckedChange={onCheckedChange}
        disabled={disabled}
        aria-label={label}
      />
    </div>
  );
}

interface LimitFieldProps {
  id: string;
  label: string;
  hint: string;
  value: number;
  onChange: (value: number) => void;
}

function LimitField({ id, label, hint, value, onChange }: LimitFieldProps) {
  return (
    <div className="space-y-2">
      <div className="flex items-baseline justify-between">
        <Label htmlFor={id}>{label}</Label>
        <span className="text-muted-foreground text-xs">{hint}</span>
      </div>
      <Input
        id={id}
        type="number"
        min={0}
        value={value}
        onChange={(event) => {
          const next = Number.parseInt(event.target.value, 10);
          onChange(Number.isFinite(next) && next > 0 ? next : 0);
        }}
      />
    </div>
  );
}
