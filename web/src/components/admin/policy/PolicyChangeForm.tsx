// Bloem: the "what should change" step shared by the organization People
// drawer and the platform bulk account page.
import { useBloemT, type BloemMessageKey } from "@/bloem/i18n";
import type { ReactNode } from "react";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { EntitlementCohort } from "@/hooks/queries/admin/entitlementCohorts";
import {
  KEEP,
  describePatchDraft,
  normalizePatchDraft,
  type PatchDraft,
  type PlanChoice,
  type PolicyChangeDraft,
  type PolicyOperation,
  type TriState,
} from "./policyDraft";
import { PLAYBACK_QUALITIES, POLICY_PERMISSIONS, type NamedLibrary } from "./policyVocabulary";

const OPERATIONS: Array<{
  value: Exclude<PolicyOperation, "">;
  label: BloemMessageKey;
  hint: BloemMessageKey;
}> = [
  {
    value: "assign",
    label: "bloem.admin.policy.changeForm.operationAssign",
    hint: "bloem.admin.policy.changeForm.operationAssignHint",
  },
  {
    value: "template",
    label: "bloem.admin.policy.changeForm.operationTemplate",
    hint: "bloem.admin.policy.changeForm.operationTemplateHint",
  },
  {
    value: "derive",
    label: "bloem.admin.policy.changeForm.operationDerive",
    hint: "bloem.admin.policy.changeForm.operationDeriveHint",
  },
  {
    value: "restore",
    label: "bloem.admin.policy.changeForm.operationRestore",
    hint: "bloem.admin.policy.changeForm.operationRestoreHint",
  },
];

export function PolicyChangeForm({
  draft,
  onChange,
  cohorts,
  plans,
  libraries,
  error,
  radioName,
}: {
  draft: PolicyChangeDraft;
  onChange(next: PolicyChangeDraft): void;
  cohorts: EntitlementCohort[];
  /** Plans to choose from. Without them the admin types the plan key. */
  plans?: PlanChoice[];
  libraries: NamedLibrary[];
  error?: string;
  radioName: string;
}) {
  const bt = useBloemT();
  const update = (patch: Partial<PolicyChangeDraft>) => onChange({ ...draft, ...patch });
  const activeCohorts = cohorts.filter((cohort) => !cohort.archived);
  const cohortLabel =
    draft.operation === "derive"
      ? bt("bloem.admin.policy.changeForm.startFromCohort")
      : bt("bloem.admin.policy.changeForm.cohort");
  return (
    <div className="space-y-5">
      <fieldset
        className="grid gap-2"
        role="radiogroup"
        aria-label={bt("bloem.admin.policy.changeForm.whatShouldChange")}
      >
        {OPERATIONS.map((option) => (
          <label
            key={option.value}
            className="border-border has-[:checked]:border-primary has-[:checked]:bg-primary/5 hover:bg-muted/40 flex cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors"
          >
            <input
              type="radio"
              name={radioName}
              aria-label={bt(option.label)}
              className="mt-1"
              checked={draft.operation === option.value}
              onChange={() => update({ operation: option.value })}
            />
            <span>
              <span className="block text-sm font-medium">{bt(option.label)}</span>
              <span className="text-muted-foreground mt-0.5 block text-sm">{bt(option.hint)}</span>
            </span>
          </label>
        ))}
      </fieldset>

      {draft.operation === "assign" || draft.operation === "derive" ? (
        <Field label={cohortLabel} htmlFor={`${radioName}-cohort`}>
          {activeCohorts.length ? (
            <select
              id={`${radioName}-cohort`}
              aria-label={cohortLabel}
              className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
              value={draft.cohortID}
              onChange={(event) => update({ cohortID: event.target.value })}
            >
              <option value="">{bt("bloem.admin.policy.changeForm.chooseACohort")}</option>
              {activeCohorts.map((cohort) => (
                <option key={cohort.cohort_id} value={cohort.cohort_id}>
                  {bt("bloem.admin.policy.changeForm.cohortOption", {
                    name: cohort.name,
                    count: cohort.member_count,
                  })}
                </option>
              ))}
            </select>
          ) : (
            <p className="text-muted-foreground text-sm">
              {bt("bloem.admin.policy.changeForm.thisOrganizationHasNoCohorts")}
            </p>
          )}
        </Field>
      ) : null}

      {draft.operation === "template" ? (
        <PlanFields draft={draft} plans={plans} radioName={radioName} onChange={update} />
      ) : null}

      {draft.operation === "derive" ? (
        <DerivedSettings
          draft={draft}
          libraries={libraries}
          radioName={radioName}
          onChange={update}
        />
      ) : null}

      {error ? (
        <p className="text-destructive text-sm" role="alert">
          {error}
        </p>
      ) : null}
    </div>
  );
}

function PlanFields({
  draft,
  plans,
  radioName,
  onChange,
}: {
  draft: PolicyChangeDraft;
  plans?: PlanChoice[];
  radioName: string;
  onChange(patch: Partial<PolicyChangeDraft>): void;
}) {
  const bt = useBloemT();
  if (!plans) {
    return (
      <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_9rem]">
        <Field
          label={bt("bloem.admin.policy.changeForm.planKey")}
          htmlFor={`${radioName}-plan-key`}
          hint={bt("bloem.admin.policy.changeForm.planKeyHint")}
        >
          <Input
            id={`${radioName}-plan-key`}
            value={draft.planKey}
            onChange={(event) => onChange({ planKey: event.target.value })}
          />
        </Field>
        <Field
          label={bt("bloem.admin.policy.changeForm.revision")}
          htmlFor={`${radioName}-plan-revision`}
        >
          <Input
            id={`${radioName}-plan-revision`}
            type="number"
            min={1}
            value={draft.planRevision}
            onChange={(event) => onChange({ planRevision: event.target.value })}
          />
        </Field>
      </div>
    );
  }
  const plan = plans.find((item) => item.key === draft.planKey);
  return (
    <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_12rem]">
      <Field label={bt("bloem.admin.policy.changeForm.plan")} htmlFor={`${radioName}-plan`}>
        <select
          id={`${radioName}-plan`}
          className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
          value={draft.planKey}
          onChange={(event) => {
            const next = plans.find((item) => item.key === event.target.value);
            onChange({
              planKey: event.target.value,
              planRevision: next ? String(next.revision) : "",
            });
          }}
        >
          <option value="">{bt("bloem.admin.policy.changeForm.chooseAPlan")}</option>
          {plans.map((item) => (
            <option key={item.key} value={item.key}>
              {item.name}
            </option>
          ))}
        </select>
      </Field>
      <Field
        label={bt("bloem.admin.policy.changeForm.revision")}
        htmlFor={`${radioName}-plan-revision`}
      >
        <select
          id={`${radioName}-plan-revision`}
          className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
          disabled={!plan}
          value={draft.planRevision}
          onChange={(event) => onChange({ planRevision: event.target.value })}
        >
          {plan ? (
            Array.from({ length: plan.revision }, (_, index) => plan.revision - index).map(
              (revision) => (
                <option key={revision} value={String(revision)}>
                  {revision === plan.revision
                    ? bt("bloem.admin.policy.changeForm.newest", { revision })
                    : String(revision)}
                </option>
              ),
            )
          ) : (
            <option value="">{bt("bloem.admin.policy.changeForm.chooseAPlanFirst")}</option>
          )}
        </select>
      </Field>
    </div>
  );
}

function DerivedSettings({
  draft,
  libraries,
  radioName,
  onChange,
}: {
  draft: PolicyChangeDraft;
  libraries: NamedLibrary[];
  radioName: string;
  onChange(patch: Partial<PolicyChangeDraft>): void;
}) {
  const bt = useBloemT();
  const patch = draft.patch;
  const setPatch = <Key extends keyof PatchDraft>(key: Key, value: PatchDraft[Key]) =>
    onChange({ patch: normalizePatchDraft({ ...patch, [key]: value }) });
  const playbackOff = patch.playback === "false";
  const changes = describePatchDraft(patch, libraries);
  return (
    <section
      className="border-border space-y-5 border-t pt-5"
      aria-label={bt("bloem.admin.policy.changeForm.settingsToChange")}
    >
      <Field
        label={bt("bloem.admin.policy.changeForm.nameForTheNewCohort")}
        htmlFor={`${radioName}-derived-name`}
        hint={bt("bloem.admin.policy.changeForm.derivedNameHint")}
      >
        <Input
          id={`${radioName}-derived-name`}
          value={draft.derivedName}
          onChange={(event) => onChange({ derivedName: event.target.value })}
        />
      </Field>

      <p className="text-muted-foreground text-sm">
        {bt("bloem.admin.policy.changeForm.anythingLeftOnKeepAs")}
      </p>

      <SetPicker
        id={`${radioName}-libraries`}
        label={bt("bloem.admin.policy.changeForm.libraries")}
        mode={patch.librariesMode}
        modes={[
          [KEEP, bt("bloem.admin.policy.changeForm.keepAsIs")],
          ["add", bt("bloem.admin.policy.changeForm.addLibraries")],
          ["remove", bt("bloem.admin.policy.changeForm.removeLibraries")],
          ["replace", bt("bloem.admin.policy.changeForm.onlyTheseLibraries")],
          ["all", bt("bloem.admin.policy.changeForm.allLibraries")],
          ["none", bt("bloem.admin.policy.changeForm.noLibraries")],
        ]}
        onModeChange={(mode) => setPatch("librariesMode", mode as PatchDraft["librariesMode"])}
        options={libraries.map((library) => [library.id, library.name] as const)}
        selected={patch.libraryIDs}
        onSelectedChange={(values) => setPatch("libraryIDs", values as number[])}
        emptyText={bt("bloem.admin.policy.changeForm.noLibrariesToChoose")}
      />

      <SetPicker
        id={`${radioName}-permissions`}
        label={bt("bloem.admin.policy.changeForm.extraPermissions")}
        mode={patch.permissionsMode}
        modes={[
          [KEEP, bt("bloem.admin.policy.changeForm.keepAsIs")],
          ["add", bt("bloem.admin.policy.changeForm.addPermissions")],
          ["remove", bt("bloem.admin.policy.changeForm.removePermissions")],
          ["replace", bt("bloem.admin.policy.changeForm.onlyThesePermissions")],
          ["unrestricted", bt("bloem.admin.policy.changeForm.allPermissions")],
        ]}
        onModeChange={(mode) => setPatch("permissionsMode", mode as PatchDraft["permissionsMode"])}
        options={POLICY_PERMISSIONS}
        selected={patch.permissions}
        onSelectedChange={(values) => setPatch("permissions", values as string[])}
        emptyText={bt("bloem.admin.policy.changeForm.noPermissionsToChoose")}
      />

      <div className="grid gap-4 sm:grid-cols-2">
        <TriStateField
          id={`${radioName}-playback`}
          label={bt("bloem.admin.policy.changeForm.playsVideo")}
          value={patch.playback}
          onChange={(value) => setPatch("playback", value)}
        />
        <TriStateField
          id={`${radioName}-transcode`}
          label={bt("bloem.admin.policy.changeForm.videoConversion")}
          value={patch.transcode}
          disabled={playbackOff}
          onChange={(value) => setPatch("transcode", value)}
        />
        <TriStateField
          id={`${radioName}-downloads`}
          label={bt("bloem.admin.policy.changeForm.downloadOriginalFiles")}
          value={patch.downloads}
          disabled={playbackOff}
          onChange={(value) => setPatch("downloads", value)}
        />
        <TriStateField
          id={`${radioName}-converted-downloads`}
          label={bt("bloem.admin.policy.changeForm.downloadConvertedCopies")}
          value={patch.transcodedDownloads}
          disabled={playbackOff || patch.downloads === "false"}
          onChange={(value) => setPatch("transcodedDownloads", value)}
        />
        <TriStateField
          id={`${radioName}-requests`}
          label={bt("bloem.admin.policy.changeForm.mediaRequests")}
          value={patch.requests}
          onChange={(value) => setPatch("requests", value)}
        />
        <Field
          label={bt("bloem.admin.policy.changeForm.bestPictureQuality")}
          htmlFor={`${radioName}-quality`}
        >
          <select
            id={`${radioName}-quality`}
            className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
            disabled={playbackOff}
            value={patch.maxPlaybackQuality}
            onChange={(event) => setPatch("maxPlaybackQuality", event.target.value)}
          >
            <option value={KEEP}>{bt("bloem.admin.policy.changeForm.keepAsIs")}</option>
            {PLAYBACK_QUALITIES.map(([value, label]) => (
              <option key={value || "any"} value={value}>
                {label}
              </option>
            ))}
          </select>
        </Field>
        <NumberField
          id={`${radioName}-streams`}
          label={bt("bloem.admin.policy.changeForm.screensAtOnce")}
          value={patch.maxStreams}
          disabled={playbackOff}
          onChange={(value) => setPatch("maxStreams", value)}
        />
        <NumberField
          id={`${radioName}-profiles`}
          label={bt("bloem.admin.policy.changeForm.profiles")}
          value={patch.maxProfiles}
          onChange={(value) => setPatch("maxProfiles", value)}
        />
        <NumberField
          id={`${radioName}-transcodes`}
          label={bt("bloem.admin.policy.changeForm.conversionsAtOnce")}
          value={patch.maxTranscodes}
          disabled={playbackOff || patch.transcode === "false"}
          onChange={(value) => setPatch("maxTranscodes", value)}
        />
      </div>

      <section
        className="border-border bg-muted/30 rounded-lg border p-3"
        aria-label={bt("bloem.admin.policy.changeForm.yourChanges")}
      >
        <h3 className="text-sm font-medium">{bt("bloem.admin.policy.changeForm.yourChanges")}</h3>
        {changes.length ? (
          <ul className="text-muted-foreground mt-2 space-y-1 text-sm">
            {changes.map((change) => (
              <li key={change}>{change}</li>
            ))}
          </ul>
        ) : (
          <p className="text-muted-foreground mt-2 text-sm">
            {bt("bloem.admin.policy.changeForm.nothingChangedYet")}
          </p>
        )}
      </section>
    </section>
  );
}

function SetPicker<Value extends string | number>({
  id,
  label,
  mode,
  modes,
  onModeChange,
  options,
  selected,
  onSelectedChange,
  emptyText,
}: {
  id: string;
  label: string;
  mode: string;
  modes: Array<[string, string]>;
  onModeChange(mode: string): void;
  options: ReadonlyArray<readonly [Value, string]>;
  selected: Value[];
  onSelectedChange(values: Value[]): void;
  emptyText: string;
}) {
  const bt = useBloemT();
  const picking = mode === "add" || mode === "remove" || mode === "replace";
  return (
    <div className="space-y-2">
      <Field label={label} htmlFor={id}>
        <select
          id={id}
          className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm sm:w-72"
          value={mode}
          onChange={(event) => onModeChange(event.target.value)}
        >
          {modes.map(([value, text]) => (
            <option key={value} value={value}>
              {text}
            </option>
          ))}
        </select>
      </Field>
      {picking ? (
        options.length ? (
          <fieldset
            aria-label={bt("bloem.admin.policy.changeForm.toPick", { label })}
            className="border-border grid max-h-56 gap-1 overflow-y-auto rounded-lg border p-2 sm:grid-cols-2"
          >
            {options.map(([value, text]) => (
              <label
                key={String(value)}
                className="hover:bg-muted/50 flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-sm transition-colors"
              >
                <input
                  type="checkbox"
                  checked={selected.includes(value)}
                  onChange={(event) =>
                    onSelectedChange(
                      event.target.checked
                        ? [...selected, value]
                        : selected.filter((item) => item !== value),
                    )
                  }
                />
                {text}
              </label>
            ))}
          </fieldset>
        ) : (
          <p className="text-muted-foreground text-sm">{emptyText}</p>
        )
      ) : null}
    </div>
  );
}

function TriStateField({
  id,
  label,
  value,
  disabled = false,
  onChange,
}: {
  id: string;
  label: string;
  value: TriState;
  disabled?: boolean;
  onChange(value: TriState): void;
}) {
  const bt = useBloemT();
  return (
    <Field label={label} htmlFor={id}>
      <select
        id={id}
        className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
        disabled={disabled}
        value={value}
        onChange={(event) => onChange(event.target.value as TriState)}
      >
        <option value={KEEP}>{bt("bloem.admin.policy.changeForm.keepAsIs")}</option>
        <option value="true">{bt("bloem.admin.policy.changeForm.allow")}</option>
        <option value="false">{bt("bloem.admin.policy.changeForm.donTAllow")}</option>
      </select>
    </Field>
  );
}

function NumberField({
  id,
  label,
  value,
  disabled = false,
  onChange,
}: {
  id: string;
  label: string;
  value: string;
  disabled?: boolean;
  onChange(value: string): void;
}) {
  const bt = useBloemT();
  return (
    <Field label={label} htmlFor={id}>
      <Input
        id={id}
        type="number"
        min={0}
        inputMode="numeric"
        placeholder={bt("bloem.admin.policy.changeForm.keepAsIs")}
        disabled={disabled}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    </Field>
  );
}

function Field({
  label,
  htmlFor,
  hint,
  children,
}: {
  label: string;
  htmlFor: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
      {hint ? <p className="text-muted-foreground text-xs">{hint}</p> : null}
    </div>
  );
}
