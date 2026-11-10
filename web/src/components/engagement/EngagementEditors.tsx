import { bloemT, useBloemT, type BloemMessageKey, type BloemTranslate } from "@/bloem/i18n";
import { useId, useState, type FormEvent, type HTMLInputTypeAttribute } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { AccountPicker } from "@/components/admin/policy/AccountPicker";
import { useAdminLibraries } from "@/hooks/queries/admin/libraries";
import { usePlatformOrganizations } from "@/hooks/queries/admin/organizations";
import { CampaignCardView } from "./CampaignCardView";
import {
  campaignImage,
  type CampaignInput,
  type CampaignTargeting,
  type SeasonalInput,
  type StoredCampaign,
  type StoredSeason,
} from "./campaigns";

type Upload = (
  file: File,
  kind: "campaign_card_16x9" | "season_banner" | "season_sprite",
) => Promise<string>;
interface EditorProps<T> {
  initial?: T;
  pending: boolean;
  storageAvailable: boolean;
  upload: Upload;
  onCancel: () => void;
}
const inputClass = "border-input bg-background h-9 w-full rounded-md border px-3 text-sm";
function text(form: FormData, name: string) {
  return String(form.get(name) ?? "").trim();
}
function list(form: FormData, name: string) {
  return text(form, name)
    .split(/[\n,]/)
    .map((value) => value.trim())
    .filter(Boolean);
}
/** `invalid` names the message saying which field must be an integer of at least `minimum`. */
function integer(value: string, invalid: BloemMessageKey, minimum = 0) {
  const number = Number(value);
  if (!value || !Number.isSafeInteger(number) || number < minimum)
    throw new Error(bloemT(invalid, { minimum }));
  return number;
}
function utc(value: string) {
  const date = new Date(`${value}Z`);
  if (!Number.isFinite(date.getTime()))
    throw new Error(bloemT("bloem.admin.engagement.editor.scheduleDates"));
  return date.toISOString();
}
function dateInput(value?: string) {
  return value ? new Date(value).toISOString().slice(0, 16) : "";
}
function Field({
  name,
  label,
  value,
  type = "text",
  required = false,
  min,
  max,
  step,
  maxLength,
}: {
  name: string;
  label: string;
  value?: string | number | null;
  type?: HTMLInputTypeAttribute;
  required?: boolean;
  min?: number;
  max?: number;
  step?: number;
  maxLength?: number;
}) {
  const id = useId();
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        name={name}
        type={type}
        defaultValue={value ?? ""}
        required={required}
        min={min}
        max={max}
        step={step}
        maxLength={maxLength}
      />
    </div>
  );
}
function Check({
  name,
  value,
  label,
  checked,
}: {
  name: string;
  value?: string;
  label: string;
  checked?: boolean;
}) {
  return (
    <label className="flex items-center gap-2 text-sm">
      <input
        className="accent-primary size-4"
        type="checkbox"
        name={name}
        value={value}
        defaultChecked={checked}
      />
      {label}
    </label>
  );
}
const SURFACE_LABELS: Record<string, BloemMessageKey> = {
  home: "bloem.admin.engagement.editor.surfaceHome",
  detail: "bloem.admin.engagement.campaign.surfaceDetail",
  pre_playback: "bloem.admin.engagement.campaign.surfacePrePlayback",
  in_playback: "bloem.admin.engagement.campaign.surfaceInPlayback",
};
/** A campaign surface's label; an unknown surface shows as stored. */
function surfaceLabel(bt: BloemTranslate, surface: string) {
  const key = SURFACE_LABELS[surface];
  return key ? bt(key) : surface;
}

const AUDIENCE_LABELS: Record<CampaignTargeting["audience"], BloemMessageKey> = {
  all: "bloem.admin.engagement.campaign.audienceAll",
  role: "bloem.admin.engagement.campaign.audienceRole",
  organization: "bloem.admin.engagement.campaign.audienceOrganization",
  library: "bloem.admin.engagement.campaign.audienceLibrary",
  explicit: "bloem.admin.engagement.campaign.audienceExplicit",
};

function formatUTC(value: string) {
  return `${value.replace("T", " ").replace(/:\d{2}(\.\d+)?Z$/, "")} UTC`;
}

/** An organization picker; empty means everywhere when `everywhere` is given. */
function OrganizationSelect({
  name,
  label,
  value,
  everywhere,
  required = false,
}: {
  name: string;
  label: string;
  value?: string | null;
  everywhere?: string;
  required?: boolean;
}) {
  const bt = useBloemT();
  const id = useId();
  const organizations = usePlatformOrganizations({ limit: 100 });
  const options = organizations.data?.organizations ?? [];
  if (organizations.isError) {
    return (
      <div className="space-y-2">
        <Label htmlFor={id}>
          {bt("bloem.admin.engagement.editor.organizationIdLabel", { label })}
        </Label>
        <Input id={id} name={name} defaultValue={value ?? ""} required={required} />
      </div>
    );
  }
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      <select
        id={id}
        name={name}
        className={inputClass}
        defaultValue={value ?? ""}
        key={options.length}
        required={required}
      >
        <option value="">
          {everywhere ?? bt("bloem.admin.engagement.editor.chooseOrganization")}
        </option>
        {value && !options.some((organization) => organization.id === value) ? (
          <option value={value}>{value}</option>
        ) : null}
        {options.map((organization) => (
          <option key={organization.id} value={organization.id}>
            {organization.name}
          </option>
        ))}
      </select>
    </div>
  );
}

function LibrarySelect({ name, label, value }: { name: string; label: string; value?: number }) {
  const bt = useBloemT();
  const id = useId();
  const libraries = useAdminLibraries();
  const options = libraries.data ?? [];
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      <select
        id={id}
        name={name}
        className={inputClass}
        defaultValue={value ? String(value) : ""}
        key={options.length}
        required
      >
        <option value="">{bt("bloem.admin.engagement.editor.chooseLibrary")}</option>
        {value && !options.some((library) => library.id === value) ? (
          <option value={String(value)}>
            {bt("bloem.admin.engagement.editor.libraryNumber", { id: String(value) })}
          </option>
        ) : null}
        {options.map((library) => (
          <option key={library.id} value={String(library.id)}>
            {library.name}
          </option>
        ))}
      </select>
    </div>
  );
}

function TimezoneField({ value }: { value: string }) {
  const bt = useBloemT();
  const id = useId();
  const zones =
    typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : [];
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{bt("bloem.admin.engagement.editor.timezone")}</Label>
      <Input id={id} name="timezone" list={`${id}-zones`} defaultValue={value} required />
      <datalist id={`${id}-zones`}>
        {zones.map((zone) => (
          <option key={zone} value={zone} />
        ))}
      </datalist>
      <p className="text-muted-foreground text-xs">
        {bt("bloem.admin.engagement.editor.timezoneHint")}
      </p>
    </div>
  );
}

function Artwork({
  value,
  setValue,
  kind,
  available,
  upload,
}: {
  value: string;
  setValue: (url: string) => void;
  kind: Parameters<Upload>[1];
  available: boolean;
  upload: Upload;
}) {
  const bt = useBloemT();
  const id = useId();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{bt("bloem.admin.engagement.editor.artwork")}</Label>
      <Input
        id={id}
        value={value}
        onChange={(event) => setValue(event.target.value)}
        placeholder={bt("bloem.admin.engagement.editor.artworkPlaceholder")}
      />
      {available ? (
        <label className="block text-sm">
          {bt("bloem.admin.engagement.editor.artworkUpload")}
          <input
            className="mt-2 block max-w-full text-sm"
            type="file"
            accept="image/png,image/jpeg,image/webp,image/gif"
            disabled={pending}
            onChange={(event) => {
              const file = event.currentTarget.files?.[0];
              event.currentTarget.value = "";
              if (!file) return;
              if (file.size > 8 * 1024 * 1024) {
                setError(bt("bloem.admin.engagement.editor.imageTooLarge"));
                return;
              }
              setPending(true);
              setError("");
              void upload(file, kind)
                .then(setValue)
                .catch((cause: unknown) =>
                  setError(
                    cause instanceof Error
                      ? cause.message
                      : bt("bloem.admin.engagement.editor.uploadFailed"),
                  ),
                )
                .finally(() => setPending(false));
            }}
          />
        </label>
      ) : (
        <p className="text-muted-foreground text-xs">
          {bt("bloem.admin.engagement.editor.uploadsUnavailable")}
        </p>
      )}
      {pending && <p role="status">{bt("bloem.admin.engagement.editor.uploading")}</p>}
      {error && (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      )}
    </div>
  );
}
function WindowFields({ start, end }: { start?: string; end?: string }) {
  const bt = useBloemT();
  return (
    <fieldset className="space-y-3">
      <legend className="font-medium">{bt("bloem.admin.engagement.editor.scheduleLegend")}</legend>
      <p className="text-muted-foreground text-sm">
        {bt("bloem.admin.engagement.editor.scheduleHelp")}
      </p>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          name="starts_at"
          label={bt("bloem.admin.engagement.editor.scheduleStarts")}
          type="datetime-local"
          value={dateInput(start)}
          required
        />
        <Field
          name="ends_at"
          label={bt("bloem.admin.engagement.editor.scheduleEnds")}
          type="datetime-local"
          value={dateInput(end)}
          required
        />
      </div>
    </fieldset>
  );
}

export function CampaignEditor({
  initial,
  pending,
  storageAvailable,
  upload,
  onCancel,
  onSave,
}: EditorProps<StoredCampaign> & { onSave: (value: CampaignInput) => Promise<void> }) {
  const bt = useBloemT();
  const [image, setImage] = useState(initial?.image_url ?? "");
  const [audience, setAudience] = useState<CampaignTargeting["audience"]>(
    initial?.targeting.audience ?? "all",
  );
  const [review, setReview] = useState<CampaignInput | null>(null);
  const [error, setError] = useState("");
  const [accountIDs, setAccountIDs] = useState<number[]>(initial?.targeting.user_ids ?? []);
  function preview(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    try {
      const form = new FormData(event.currentTarget);
      const targeting: CampaignTargeting = { audience };
      if (audience === "role") targeting.role = text(form, "role");
      if (audience === "organization")
        targeting.organization_id = text(form, "target_organization");
      if (audience === "library")
        targeting.library_id = integer(
          text(form, "library_id"),
          "bloem.admin.engagement.editor.integerLibrary",
          1,
        );
      if (audience === "explicit") {
        targeting.user_ids = [...accountIDs];
        targeting.profile_ids = list(form, "profile_ids");
        if (!targeting.user_ids.length && !targeting.profile_ids.length)
          throw new Error(bt("bloem.admin.engagement.campaign.chooseAccountOrProfile"));
      }
      const startsAt = utc(text(form, "starts_at")),
        endsAt = utc(text(form, "ends_at"));
      if (startsAt >= endsAt) throw new Error(bt("bloem.admin.engagement.editor.endBeforeStart"));
      if (!campaignImage(image))
        throw new Error(bt("bloem.admin.engagement.campaign.imageRequired"));
      const surfaces = form.getAll("surface").map(String);
      if (!surfaces.length) throw new Error(bt("bloem.admin.engagement.campaign.chooseSurface"));
      const style = text(form, "playback_style");
      const dismissible = form.has("dismissible");
      if (surfaces.includes("in_playback") && (!style || !dismissible))
        throw new Error(bt("bloem.admin.engagement.campaign.playbackNeedsStyle"));
      setReview({
        organization_id: text(form, "organization_id") || null,
        surfaces,
        kicker: text(form, "kicker"),
        headline: text(form, "headline"),
        subtitle: text(form, "subtitle"),
        image_url: image.trim(),
        image_width: text(form, "image_width")
          ? integer(
              text(form, "image_width"),
              "bloem.admin.engagement.editor.integerArtworkWidth",
              1,
            )
          : null,
        image_height: text(form, "image_height")
          ? integer(
              text(form, "image_height"),
              "bloem.admin.engagement.editor.integerArtworkHeight",
              1,
            )
          : null,
        deeplink: text(form, "deeplink"),
        cta:
          text(form, "cta_label") || text(form, "cta_url")
            ? { label: text(form, "cta_label"), url: text(form, "cta_url") }
            : null,
        priority: integer(
          text(form, "priority"),
          "bloem.admin.engagement.editor.integerPriority",
          -2147483648,
        ),
        starts_at: startsAt,
        ends_at: endsAt,
        targeting,
        dismissible,
        placement: {
          ...initial?.placement,
          home_position: integer(
            text(form, "home_position"),
            "bloem.admin.engagement.editor.integerHomePosition",
          ),
          detail_slot: text(form, "detail_slot"),
          content_ids: list(form, "content_ids"),
          playback_style: style,
          video_url: text(form, "video_url"),
          duration_seconds: style
            ? integer(
                text(form, "duration_seconds"),
                "bloem.admin.engagement.editor.integerOverlayDuration",
                5,
              )
            : 0,
        },
      });
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : bt("bloem.admin.engagement.campaign.checkFields"),
      );
    }
  }
  return (
    <div className="space-y-5">
      <form
        onSubmit={preview}
        onChange={() => setReview(null)}
        className="space-y-6"
        aria-label={bt("bloem.admin.engagement.campaign.editorLabel")}
      >
        <fieldset disabled={pending} className="space-y-6">
          <h2 className="text-xl font-medium">
            {initial
              ? bt("bloem.admin.engagement.campaign.editTitle")
              : bt("bloem.admin.engagement.campaign.newTitle")}
          </h2>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="headline"
              label={bt("bloem.admin.engagement.campaign.headline")}
              value={initial?.headline}
              required
              maxLength={120}
            />
            <Field
              name="kicker"
              label={bt("bloem.admin.engagement.campaign.kicker")}
              value={initial?.kicker}
              maxLength={40}
            />
            <Field
              name="subtitle"
              label={bt("bloem.admin.engagement.campaign.subtitle")}
              value={initial?.subtitle}
              maxLength={200}
            />
            <OrganizationSelect
              name="organization_id"
              label={bt("bloem.admin.engagement.editor.belongsTo")}
              value={initial?.organization_id}
              everywhere={bt("bloem.admin.engagement.editor.wholeServer")}
            />
          </div>
          <Artwork
            value={image}
            setValue={(value) => {
              setImage(value);
              setReview(null);
            }}
            kind="campaign_card_16x9"
            available={storageAvailable}
            upload={upload}
          />
          <p className="text-muted-foreground text-sm">
            {bt("bloem.admin.engagement.campaign.artworkRatio")}
          </p>
          <fieldset className="space-y-3">
            <legend className="font-medium">
              {bt("bloem.admin.engagement.campaign.whereItShows")}
            </legend>
            <div className="flex flex-wrap gap-4">
              {["home", "detail", "pre_playback", "in_playback"].map((surface) => (
                <Check
                  key={surface}
                  name="surface"
                  value={surface}
                  label={surfaceLabel(bt, surface)}
                  checked={initial ? initial.surfaces.includes(surface) : surface === "home"}
                />
              ))}
            </div>
          </fieldset>
          <Field
            name="home_position"
            label={bt("bloem.admin.engagement.campaign.homePosition")}
            type="number"
            min={0}
            value={initial?.placement.home_position ?? 1}
          />
          <label className="block space-y-2 text-sm">
            {bt("bloem.admin.engagement.campaign.playbackStyle")}
            <select
              name="playback_style"
              className={inputClass}
              defaultValue={initial?.placement.playback_style ?? ""}
            >
              <option value="">{bt("bloem.admin.engagement.campaign.playbackStyleNone")}</option>
              <option value="card">
                {bt("bloem.admin.engagement.campaign.playbackStyleCard")}
              </option>
              <option value="pip">{bt("bloem.admin.engagement.campaign.playbackStylePip")}</option>
            </select>
          </label>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="duration_seconds"
              label={bt("bloem.admin.engagement.campaign.durationSeconds")}
              type="number"
              min={5}
              max={60}
              value={initial?.placement.duration_seconds || 10}
            />
            <Field
              name="video_url"
              label={bt("bloem.admin.engagement.campaign.videoUrl")}
              value={initial?.placement.video_url}
            />
          </div>
          <WindowFields start={initial?.starts_at} end={initial?.ends_at} />
          <fieldset className="space-y-4">
            <legend className="font-medium">
              {bt("bloem.admin.engagement.campaign.audience")}
            </legend>
            <label className="block space-y-2 text-sm">
              {bt("bloem.admin.engagement.campaign.whoSeesIt")}
              <select
                name="audience"
                className={inputClass}
                value={audience}
                onChange={(event) =>
                  setAudience(event.target.value as CampaignTargeting["audience"])
                }
              >
                {(["all", "role", "organization", "library", "explicit"] as const).map((value) => (
                  <option key={value} value={value}>
                    {bt(AUDIENCE_LABELS[value])}
                  </option>
                ))}
              </select>
            </label>
            {audience === "role" && (
              <label className="block space-y-2 text-sm">
                {bt("bloem.admin.engagement.campaign.accountRole")}
                <select
                  name="role"
                  className={inputClass}
                  defaultValue={initial?.targeting.role ?? "user"}
                >
                  <option value="user">{bt("bloem.admin.engagement.campaign.roleMembers")}</option>
                  <option value="admin">
                    {bt("bloem.admin.engagement.campaign.roleAdministrators")}
                  </option>
                </select>
              </label>
            )}
            {audience === "organization" && (
              <OrganizationSelect
                name="target_organization"
                label={bt("bloem.admin.engagement.campaign.organization")}
                value={initial?.targeting.organization_id}
                required
              />
            )}
            {audience === "library" && (
              <LibrarySelect
                name="library_id"
                label={bt("bloem.admin.engagement.campaign.library")}
                value={initial?.targeting.library_id}
              />
            )}
            {audience === "explicit" && (
              <div className="space-y-4">
                <AccountPicker
                  id="campaign-accounts"
                  label={bt("bloem.admin.engagement.campaign.accounts")}
                  selected={accountIDs}
                  onChange={(ids) => {
                    setAccountIDs(ids);
                    setReview(null);
                  }}
                />
                <Field
                  name="profile_ids"
                  label={bt("bloem.admin.engagement.campaign.profileIds")}
                  value={initial?.targeting.profile_ids?.join(", ")}
                />
              </div>
            )}
          </fieldset>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="cta_label"
              label={bt("bloem.admin.engagement.campaign.ctaLabel")}
              value={initial?.cta?.label}
              maxLength={40}
            />
            <Field
              name="cta_url"
              label={bt("bloem.admin.engagement.campaign.ctaUrl")}
              value={initial?.cta?.url}
            />
          </div>
          <details className="space-y-4">
            <summary className="text-muted-foreground hover:text-foreground cursor-pointer text-sm transition-colors">
              {bt("bloem.admin.engagement.campaign.moreOptions")}
            </summary>
            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              <Field
                name="deeplink"
                label={bt("bloem.admin.engagement.campaign.deeplink")}
                value={initial?.deeplink}
              />
              <Field
                name="priority"
                label={bt("bloem.admin.engagement.campaign.priority")}
                type="number"
                value={initial?.priority ?? 0}
              />
              <Field
                name="content_ids"
                label={bt("bloem.admin.engagement.campaign.contentIds")}
                value={initial?.placement.content_ids?.join(", ")}
              />
              <Field
                name="detail_slot"
                label={bt("bloem.admin.engagement.campaign.detailSlot")}
                value={initial?.placement.detail_slot}
              />
              <Field
                name="image_width"
                label={bt("bloem.admin.engagement.campaign.imageWidth")}
                type="number"
                min={1}
                value={initial?.image_width}
              />
              <Field
                name="image_height"
                label={bt("bloem.admin.engagement.campaign.imageHeight")}
                type="number"
                min={1}
                value={initial?.image_height}
              />
            </div>
          </details>
          <Check
            name="dismissible"
            label={bt("bloem.admin.engagement.campaign.dismissible")}
            checked={initial?.dismissible ?? true}
          />
          <div className="flex gap-3">
            <Button type="submit">{bt("bloem.admin.engagement.campaign.check")}</Button>
            <Button type="button" variant="outline" onClick={onCancel}>
              {bt("bloem.admin.engagement.editor.cancel")}
            </Button>
          </div>
        </fieldset>
      </form>
      {error && (
        <p role="alert" className="text-destructive">
          {error}
        </p>
      )}
      {review && (
        <section
          className="border-border space-y-4 border-t pt-4"
          aria-label={bt("bloem.admin.engagement.campaign.reviewLabel")}
        >
          <h3 className="font-medium">
            {bt("bloem.admin.engagement.editor.checkBeforePublishing")}
          </h3>
          <p className="text-muted-foreground text-sm">
            {bt("bloem.admin.engagement.campaign.reviewSummary", {
              audience: bt(AUDIENCE_LABELS[review.targeting.audience]),
              surfaces: review.surfaces.map((surface) => surfaceLabel(bt, surface)).join(", "),
              startsAt: formatUTC(review.starts_at),
              endsAt: formatUTC(review.ends_at),
            })}
          </p>
          <div className="max-w-lg">
            <CampaignCardView
              card={{ ...review, id: initial?.id ?? "preview", expires_at: review.ends_at }}
            />
          </div>
          <Button disabled={pending} onClick={() => void onSave(review).catch(() => undefined)}>
            {pending
              ? bt("bloem.admin.engagement.editor.publishing")
              : initial
                ? bt("bloem.admin.engagement.campaign.saveChanges")
                : bt("bloem.admin.engagement.campaign.publish")}
          </Button>
        </section>
      )}
    </div>
  );
}

export function SeasonEditor({
  initial,
  pending,
  storageAvailable,
  upload,
  onCancel,
  onSave,
  yearlyAvailable,
}: EditorProps<StoredSeason> & {
  onSave: (value: SeasonalInput) => Promise<void>;
  yearlyAvailable: boolean;
}) {
  const bt = useBloemT();
  const [banner, setBanner] = useState(initial?.assets.banner_url ?? "");
  const [sprites, setSprites] = useState(initial?.assets.sprites?.join("\n") ?? "");
  const [spriteUpload, setSpriteUpload] = useState(false);
  const [review, setReview] = useState<SeasonalInput | null>(null);
  const [error, setError] = useState("");
  function preview(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    try {
      const form = new FormData(event.currentTarget);
      const startsAt = utc(text(form, "starts_at")),
        endsAt = utc(text(form, "ends_at"));
      if (startsAt >= endsAt) throw new Error(bt("bloem.admin.engagement.editor.endBeforeStart"));
      const intensity = Number(text(form, "intensity"));
      if (!Number.isFinite(intensity) || intensity < 0 || intensity > 1)
        throw new Error(bt("bloem.admin.engagement.season.intensityRange"));
      const assets = sprites
        .split(/[\n,]/)
        .map((value) => value.trim())
        .filter(Boolean);
      if ((banner && !campaignImage(banner)) || assets.some((url) => !campaignImage(url)))
        throw new Error(bt("bloem.admin.engagement.season.artworkUrls"));
      const timezone = text(form, "timezone") || "UTC";
      new Intl.DateTimeFormat("en", { timeZone: timezone }).format();
      setReview({
        effect_id: text(form, "effect_id"),
        intensity,
        organization_id: text(form, "organization_id") || null,
        surfaces: form.getAll("surface").map(String),
        assets: { banner_url: banner.trim(), sprites: assets },
        window: {
          starts_at: startsAt,
          ends_at: endsAt,
          repeat_yearly: yearlyAvailable && form.has("repeat_yearly"),
          timezone,
        },
      });
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : bt("bloem.admin.engagement.season.checkFields"),
      );
    }
  }
  return (
    <div className="space-y-5">
      <form
        onSubmit={preview}
        onChange={() => setReview(null)}
        className="space-y-6"
        aria-label={bt("bloem.admin.engagement.season.editorLabel")}
      >
        <fieldset disabled={pending || spriteUpload} className="space-y-6">
          <h2 className="text-xl font-medium">
            {initial
              ? bt("bloem.admin.engagement.season.editTitle")
              : bt("bloem.admin.engagement.season.newTitle")}
          </h2>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="effect_id"
              label={bt("bloem.admin.engagement.season.effect")}
              value={initial?.effect_id ?? "snow"}
              required
              maxLength={64}
            />
            <OrganizationSelect
              name="organization_id"
              label={bt("bloem.admin.engagement.editor.belongsTo")}
              value={initial?.organization_id}
              everywhere={bt("bloem.admin.engagement.editor.wholeServer")}
            />
            <Field
              name="intensity"
              label={bt("bloem.admin.engagement.season.intensity")}
              type="number"
              min={0}
              max={1}
              step={0.05}
              value={initial?.intensity ?? 0.5}
            />
          </div>
          <WindowFields start={initial?.window.starts_at} end={initial?.window.ends_at} />
          {yearlyAvailable && (
            <>
              <Check
                name="repeat_yearly"
                label={bt("bloem.admin.engagement.season.repeatYearly")}
                checked={initial?.window.repeat_yearly}
              />
              <TimezoneField value={initial?.window.timezone ?? "UTC"} />
            </>
          )}
          <fieldset className="space-y-3">
            <legend className="font-medium">
              {bt("bloem.admin.engagement.season.whereItShows")}
            </legend>
            <div className="flex flex-wrap gap-4">
              {["all", "home", "login"].map((surface) => (
                <Check
                  key={surface}
                  name="surface"
                  value={surface}
                  label={
                    surface === "all"
                      ? bt("bloem.admin.engagement.season.surfaceAll")
                      : surface === "home"
                        ? bt("bloem.admin.engagement.editor.surfaceHome")
                        : bt("bloem.admin.engagement.season.surfaceLogin")
                  }
                  checked={initial ? initial.surfaces.includes(surface) : surface === "all"}
                />
              ))}
            </div>
          </fieldset>
          <Artwork
            value={banner}
            setValue={(value) => {
              setBanner(value);
              setReview(null);
            }}
            kind="season_banner"
            available={storageAvailable}
            upload={upload}
          />
          <label className="block space-y-2 text-sm">
            {bt("bloem.admin.engagement.season.sprites")}
            <textarea
              className="border-input bg-background min-h-24 w-full rounded-md border p-3"
              value={sprites}
              onChange={(event) => setSprites(event.target.value)}
            />
          </label>
          {storageAvailable && (
            <label className="block text-sm">
              {bt("bloem.admin.engagement.season.uploadSprite")}
              <input
                type="file"
                accept="image/png,image/jpeg,image/webp,image/gif"
                className="mt-2 block max-w-full text-sm"
                onChange={(event) => {
                  const file = event.currentTarget.files?.[0];
                  event.currentTarget.value = "";
                  if (!file) return;
                  if (file.size > 8 * 1024 * 1024) {
                    setError(bt("bloem.admin.engagement.editor.imageTooLarge"));
                    return;
                  }
                  setSpriteUpload(true);
                  setReview(null);
                  void upload(file, "season_sprite")
                    .then((url) => setSprites((prior) => `${prior}${prior ? "\n" : ""}${url}`))
                    .catch((cause: unknown) =>
                      setError(
                        cause instanceof Error
                          ? cause.message
                          : bt("bloem.admin.engagement.editor.uploadFailed"),
                      ),
                    )
                    .finally(() => setSpriteUpload(false));
                }}
              />
            </label>
          )}
          <div className="flex gap-3">
            <Button type="submit">{bt("bloem.admin.engagement.season.check")}</Button>
            <Button type="button" variant="outline" onClick={onCancel}>
              {bt("bloem.admin.engagement.editor.cancel")}
            </Button>
          </div>
        </fieldset>
      </form>
      {error && (
        <p role="alert" className="text-destructive">
          {error}
        </p>
      )}
      {review && (
        <section
          className="border-border space-y-3 border-t pt-4"
          aria-label={bt("bloem.admin.engagement.season.reviewLabel")}
        >
          <h3 className="font-medium">
            {bt("bloem.admin.engagement.editor.checkBeforePublishing")}
          </h3>
          <p className="text-sm">
            {bt("bloem.admin.engagement.season.reviewSummary", {
              effect: review.effect_id,
              intensity: review.intensity,
              surfaces: review.surfaces.join(", ") || "all",
              repeat: review.window.repeat_yearly ? "yearly" : "once",
              timezone: review.window.timezone,
            })}
          </p>
          <p className="text-muted-foreground text-sm">
            {bt("bloem.admin.engagement.season.reviewWindow", {
              startsAt: review.window.starts_at,
              endsAt: review.window.ends_at,
            })}
          </p>
          {campaignImage(banner) && (
            <img
              className="max-h-48 max-w-full rounded-lg object-contain"
              src={banner}
              alt={bt("bloem.admin.engagement.season.bannerPreview")}
            />
          )}
          <Button
            disabled={pending || spriteUpload}
            onClick={() => void onSave(review).catch(() => undefined)}
          >
            {pending
              ? bt("bloem.admin.engagement.editor.publishing")
              : initial
                ? bt("bloem.admin.engagement.season.saveChanges")
                : bt("bloem.admin.engagement.season.publish")}
          </Button>
        </section>
      )}
    </div>
  );
}
