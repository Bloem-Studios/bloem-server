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
function integer(value: string, label: string, minimum = 0) {
  const number = Number(value);
  if (!value || !Number.isSafeInteger(number) || number < minimum)
    throw new Error(`${label} must be an integer of at least ${minimum}.`);
  return number;
}
function utc(value: string) {
  const date = new Date(`${value}Z`);
  if (!Number.isFinite(date.getTime())) throw new Error("Enter both schedule dates in UTC.");
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
const SURFACE_LABELS: Record<string, string> = {
  home: "Home screen",
  detail: "Title pages",
  pre_playback: "Before playback",
  in_playback: "During playback",
};

const AUDIENCE_LABELS: Record<CampaignTargeting["audience"], string> = {
  all: "Everyone",
  role: "By account role",
  organization: "One organization",
  library: "People who can see a library",
  explicit: "Chosen accounts or profiles",
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
  const id = useId();
  const organizations = usePlatformOrganizations({ limit: 100 });
  const options = organizations.data?.organizations ?? [];
  if (organizations.isError) {
    return (
      <div className="space-y-2">
        <Label htmlFor={id}>{label} (organization ID)</Label>
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
        <option value="">{everywhere ?? "Choose an organization"}</option>
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
        <option value="">Choose a library</option>
        {value && !options.some((library) => library.id === value) ? (
          <option value={String(value)}>Library {value}</option>
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
  const id = useId();
  const zones =
    typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : [];
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>Time zone for the yearly repeat</Label>
      <Input id={id} name="timezone" list={`${id}-zones`} defaultValue={value} required />
      <datalist id={`${id}-zones`}>
        {zones.map((zone) => (
          <option key={zone} value={zone} />
        ))}
      </datalist>
      <p className="text-muted-foreground text-xs">Start typing a city, like Europe/London.</p>
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
  const id = useId();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>Artwork</Label>
      <Input
        id={id}
        value={value}
        onChange={(event) => setValue(event.target.value)}
        placeholder="https://… or an uploaded image"
      />
      {available ? (
        <label className="block text-sm">
          Or upload an image (PNG, JPEG, WebP or GIF, up to 8 MB)
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
                setError("Images can be up to 8 MB.");
                return;
              }
              setPending(true);
              setError("");
              void upload(file, kind)
                .then(setValue)
                .catch((cause: unknown) =>
                  setError(cause instanceof Error ? cause.message : "Upload failed."),
                )
                .finally(() => setPending(false));
            }}
          />
        </label>
      ) : (
        <p className="text-muted-foreground text-xs">
          Uploads aren’t set up on this server. Use an https image address instead.
        </p>
      )}
      {pending && <p role="status">Uploading…</p>}
      {error && (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      )}
    </div>
  );
}
function WindowFields({ start, end }: { start?: string; end?: string }) {
  return (
    <fieldset className="space-y-3">
      <legend className="font-medium">Schedule (UTC)</legend>
      <p className="text-muted-foreground text-sm">
        It shows from the start time until just before the end time. A campaign never makes anyone
        wait to watch.
      </p>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          name="starts_at"
          label="Starts (UTC)"
          type="datetime-local"
          value={dateInput(start)}
          required
        />
        <Field
          name="ends_at"
          label="Ends (UTC)"
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
        targeting.library_id = integer(text(form, "library_id"), "The library", 1);
      if (audience === "explicit") {
        targeting.user_ids = [...accountIDs];
        targeting.profile_ids = list(form, "profile_ids");
        if (!targeting.user_ids.length && !targeting.profile_ids.length)
          throw new Error("Choose at least one account or profile.");
      }
      const startsAt = utc(text(form, "starts_at")),
        endsAt = utc(text(form, "ends_at"));
      if (startsAt >= endsAt) throw new Error("The end must be after the start.");
      if (!campaignImage(image)) throw new Error("Use an https image address or upload an image.");
      const surfaces = form.getAll("surface").map(String);
      if (!surfaces.length) throw new Error("Choose at least one place to show it.");
      const style = text(form, "playback_style");
      const dismissible = form.has("dismissible");
      if (surfaces.includes("in_playback") && (!style || !dismissible))
        throw new Error(
          "To show it during playback, choose how it looks and let viewers dismiss it.",
        );
      setReview({
        organization_id: text(form, "organization_id") || null,
        surfaces,
        kicker: text(form, "kicker"),
        headline: text(form, "headline"),
        subtitle: text(form, "subtitle"),
        image_url: image.trim(),
        image_width: text(form, "image_width")
          ? integer(text(form, "image_width"), "Artwork width", 1)
          : null,
        image_height: text(form, "image_height")
          ? integer(text(form, "image_height"), "Artwork height", 1)
          : null,
        deeplink: text(form, "deeplink"),
        cta:
          text(form, "cta_label") || text(form, "cta_url")
            ? { label: text(form, "cta_label"), url: text(form, "cta_url") }
            : null,
        priority: integer(text(form, "priority"), "Priority", -2147483648),
        starts_at: startsAt,
        ends_at: endsAt,
        targeting,
        dismissible,
        placement: {
          ...initial?.placement,
          home_position: integer(text(form, "home_position"), "Home position"),
          detail_slot: text(form, "detail_slot"),
          content_ids: list(form, "content_ids"),
          playback_style: style,
          video_url: text(form, "video_url"),
          duration_seconds: style
            ? integer(text(form, "duration_seconds"), "Overlay duration", 5)
            : 0,
        },
      });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Check the campaign fields.");
    }
  }
  return (
    <div className="space-y-5">
      <form
        onSubmit={preview}
        onChange={() => setReview(null)}
        className="space-y-6"
        aria-label="Campaign editor"
      >
        <fieldset disabled={pending} className="space-y-6">
          <h2 className="text-xl font-medium">{initial ? "Edit campaign" : "New campaign"}</h2>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="headline"
              label="Headline"
              value={initial?.headline}
              required
              maxLength={120}
            />
            <Field
              name="kicker"
              label="Small label above the headline"
              value={initial?.kicker}
              maxLength={40}
            />
            <Field name="subtitle" label="Subtitle" value={initial?.subtitle} maxLength={200} />
            <OrganizationSelect
              name="organization_id"
              label="Belongs to"
              value={initial?.organization_id}
              everywhere="The whole server"
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
          <p className="text-muted-foreground text-sm">Use 16:9 artwork.</p>
          <fieldset className="space-y-3">
            <legend className="font-medium">Where it shows</legend>
            <div className="flex flex-wrap gap-4">
              {["home", "detail", "pre_playback", "in_playback"].map((surface) => (
                <Check
                  key={surface}
                  name="surface"
                  value={surface}
                  label={SURFACE_LABELS[surface] ?? surface}
                  checked={initial ? initial.surfaces.includes(surface) : surface === "home"}
                />
              ))}
            </div>
          </fieldset>
          <Field
            name="home_position"
            label="Row on the home screen (0 is the top)"
            type="number"
            min={0}
            value={initial?.placement.home_position ?? 1}
          />
          <label className="block space-y-2 text-sm">
            How it looks during playback
            <select
              name="playback_style"
              className={inputClass}
              defaultValue={initial?.placement.playback_style ?? ""}
            >
              <option value="">Not shown during playback</option>
              <option value="card">A card</option>
              <option value="pip">A small muted video</option>
            </select>
          </label>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="duration_seconds"
              label="Seconds on screen (5 to 60)"
              type="number"
              min={5}
              max={60}
              value={initial?.placement.duration_seconds || 10}
            />
            <Field
              name="video_url"
              label="Small video address (https, optional)"
              value={initial?.placement.video_url}
            />
          </div>
          <WindowFields start={initial?.starts_at} end={initial?.ends_at} />
          <fieldset className="space-y-4">
            <legend className="font-medium">Audience</legend>
            <label className="block space-y-2 text-sm">
              Who sees it
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
                    {AUDIENCE_LABELS[value]}
                  </option>
                ))}
              </select>
            </label>
            {audience === "role" && (
              <label className="block space-y-2 text-sm">
                Account role
                <select
                  name="role"
                  className={inputClass}
                  defaultValue={initial?.targeting.role ?? "user"}
                >
                  <option value="user">Members</option>
                  <option value="admin">Administrators</option>
                </select>
              </label>
            )}
            {audience === "organization" && (
              <OrganizationSelect
                name="target_organization"
                label="Organization"
                value={initial?.targeting.organization_id}
                required
              />
            )}
            {audience === "library" && (
              <LibrarySelect
                name="library_id"
                label="Library"
                value={initial?.targeting.library_id}
              />
            )}
            {audience === "explicit" && (
              <div className="space-y-4">
                <AccountPicker
                  id="campaign-accounts"
                  label="Accounts"
                  selected={accountIDs}
                  onChange={(ids) => {
                    setAccountIDs(ids);
                    setReview(null);
                  }}
                />
                <Field
                  name="profile_ids"
                  label="Profile IDs, separated by commas (optional)"
                  value={initial?.targeting.profile_ids?.join(", ")}
                />
              </div>
            )}
          </fieldset>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="cta_label"
              label="Button text (optional)"
              value={initial?.cta?.label}
              maxLength={40}
            />
            <Field
              name="cta_url"
              label="Button link (https, app path or bloem://)"
              value={initial?.cta?.url}
            />
          </div>
          <details className="space-y-4">
            <summary className="text-muted-foreground hover:text-foreground cursor-pointer text-sm transition-colors">
              More options
            </summary>
            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              <Field
                name="deeplink"
                label="Opens when tapped (optional)"
                value={initial?.deeplink}
              />
              <Field
                name="priority"
                label="Priority (higher shows first)"
                type="number"
                value={initial?.priority ?? 0}
              />
              <Field
                name="content_ids"
                label="Only on these titles (content IDs, separated by commas)"
                value={initial?.placement.content_ids?.join(", ")}
              />
              <Field
                name="detail_slot"
                label="Spot on title pages (optional)"
                value={initial?.placement.detail_slot}
              />
              <Field
                name="image_width"
                label="Image width in pixels (optional)"
                type="number"
                min={1}
                value={initial?.image_width}
              />
              <Field
                name="image_height"
                label="Image height in pixels (optional)"
                type="number"
                min={1}
                value={initial?.image_height}
              />
            </div>
          </details>
          <Check
            name="dismissible"
            label="Allow viewers to dismiss"
            checked={initial?.dismissible ?? true}
          />
          <div className="flex gap-3">
            <Button type="submit">Check campaign</Button>
            <Button type="button" variant="outline" onClick={onCancel}>
              Cancel
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
        <section className="border-border space-y-4 border-t pt-4" aria-label="Campaign review">
          <h3 className="font-medium">Check it before publishing</h3>
          <p className="text-muted-foreground text-sm">
            Who sees it: {AUDIENCE_LABELS[review.targeting.audience]}. Where:{" "}
            {review.surfaces.map((surface) => SURFACE_LABELS[surface] ?? surface).join(", ")}. From{" "}
            {formatUTC(review.starts_at)} to {formatUTC(review.ends_at)}.
          </p>
          <div className="max-w-lg">
            <CampaignCardView
              card={{ ...review, id: initial?.id ?? "preview", expires_at: review.ends_at }}
            />
          </div>
          <Button disabled={pending} onClick={() => void onSave(review).catch(() => undefined)}>
            {pending ? "Publishing…" : initial ? "Save campaign changes" : "Publish campaign"}
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
      if (startsAt >= endsAt) throw new Error("The end must be after the start.");
      const intensity = Number(text(form, "intensity"));
      if (!Number.isFinite(intensity) || intensity < 0 || intensity > 1)
        throw new Error("Intensity must be between 0 and 1.");
      const assets = sprites
        .split(/[\n,]/)
        .map((value) => value.trim())
        .filter(Boolean);
      if ((banner && !campaignImage(banner)) || assets.some((url) => !campaignImage(url)))
        throw new Error("Artwork must use HTTPS or uploaded server asset paths.");
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
      setError(cause instanceof Error ? cause.message : "Check the seasonal fields.");
    }
  }
  return (
    <div className="space-y-5">
      <form
        onSubmit={preview}
        onChange={() => setReview(null)}
        className="space-y-6"
        aria-label="Seasonal pack editor"
      >
        <fieldset disabled={pending || spriteUpload} className="space-y-6">
          <h2 className="text-xl font-medium">
            {initial ? "Edit seasonal pack" : "New seasonal pack"}
          </h2>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="effect_id"
              label="Effect (snow, or the name of an artwork pack)"
              value={initial?.effect_id ?? "snow"}
              required
              maxLength={64}
            />
            <OrganizationSelect
              name="organization_id"
              label="Belongs to"
              value={initial?.organization_id}
              everywhere="The whole server"
            />
            <Field
              name="intensity"
              label="Strength (0 is off, 1 is full)"
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
                label="Repeat every year"
                checked={initial?.window.repeat_yearly}
              />
              <TimezoneField value={initial?.window.timezone ?? "UTC"} />
            </>
          )}
          <fieldset className="space-y-3">
            <legend className="font-medium">Where it shows (none means everywhere)</legend>
            <div className="flex flex-wrap gap-4">
              {["all", "home", "login"].map((surface) => (
                <Check
                  key={surface}
                  name="surface"
                  value={surface}
                  label={
                    surface === "all"
                      ? "Everywhere"
                      : surface === "home"
                        ? "Home screen"
                        : "Sign-in page"
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
            Small images that drift across the screen (one address per line, up to 32)
            <textarea
              className="border-input bg-background min-h-24 w-full rounded-md border p-3"
              value={sprites}
              onChange={(event) => setSprites(event.target.value)}
            />
          </label>
          {storageAvailable && (
            <label className="block text-sm">
              Upload a drifting image
              <input
                type="file"
                accept="image/png,image/jpeg,image/webp,image/gif"
                className="mt-2 block max-w-full text-sm"
                onChange={(event) => {
                  const file = event.currentTarget.files?.[0];
                  event.currentTarget.value = "";
                  if (!file) return;
                  if (file.size > 8 * 1024 * 1024) {
                    setError("Images can be up to 8 MB.");
                    return;
                  }
                  setSpriteUpload(true);
                  setReview(null);
                  void upload(file, "season_sprite")
                    .then((url) => setSprites((prior) => `${prior}${prior ? "\n" : ""}${url}`))
                    .catch((cause: unknown) =>
                      setError(cause instanceof Error ? cause.message : "Upload failed."),
                    )
                    .finally(() => setSpriteUpload(false));
                }}
              />
            </label>
          )}
          <div className="flex gap-3">
            <Button type="submit">Check seasonal pack</Button>
            <Button type="button" variant="outline" onClick={onCancel}>
              Cancel
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
          aria-label="Seasonal pack review"
        >
          <h3 className="font-medium">Check it before publishing</h3>
          <p className="text-sm">
            {review.effect_id} · Intensity {review.intensity} ·{" "}
            {review.surfaces.join(", ") || "all"} ·{" "}
            {review.window.repeat_yearly ? `Annual (${review.window.timezone})` : "One-time"}
          </p>
          <p className="text-muted-foreground text-sm">
            {review.window.starts_at} to {review.window.ends_at}. Viewers can opt out; playback and
            reduced-motion preferences take priority.
          </p>
          {campaignImage(banner) && (
            <img
              className="max-h-48 max-w-full rounded-lg object-contain"
              src={banner}
              alt="Seasonal banner preview"
            />
          )}
          <Button
            disabled={pending || spriteUpload}
            onClick={() => void onSave(review).catch(() => undefined)}
          >
            {pending ? "Publishing…" : initial ? "Save seasonal changes" : "Publish seasonal pack"}
          </Button>
        </section>
      )}
    </div>
  );
}
