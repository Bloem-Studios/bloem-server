import { useId, useState, type FormEvent, type HTMLInputTypeAttribute } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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
      <Label htmlFor={id}>Artwork URL</Label>
      <Input
        id={id}
        value={value}
        onChange={(event) => setValue(event.target.value)}
        placeholder="https://… or a server asset path"
      />
      {available ? (
        <label className="block text-sm">
          Upload artwork (PNG, JPEG, WebP or GIF, up to 8 MiB)
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
                setError("Artwork must be at most 8 MiB.");
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
          Upload storage is not configured. You can use an HTTPS artwork URL.
        </p>
      )}
      {pending && <p role="status">Uploading artwork…</p>}
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
        Start is inclusive; end is exclusive. A campaign is never a forced playback wait.
      </p>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          name="starts_at"
          label="Starts at (UTC)"
          type="datetime-local"
          value={dateInput(start)}
          required
        />
        <Field
          name="ends_at"
          label="Ends at (UTC)"
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
        targeting.library_id = integer(text(form, "library_id"), "Library ID", 1);
      if (audience === "explicit") {
        targeting.user_ids = list(form, "user_ids").map((id) => integer(id, "Account ID", 1));
        targeting.profile_ids = list(form, "profile_ids");
        if (!targeting.user_ids.length && !targeting.profile_ids.length)
          throw new Error("Choose at least one explicit account or profile.");
      }
      const startsAt = utc(text(form, "starts_at")),
        endsAt = utc(text(form, "ends_at"));
      if (startsAt >= endsAt) throw new Error("The end must be after the start.");
      if (!campaignImage(image))
        throw new Error("Choose HTTPS artwork or an uploaded server image.");
      const surfaces = form.getAll("surface").map(String);
      if (!surfaces.length) throw new Error("Choose at least one placement.");
      const style = text(form, "playback_style");
      const dismissible = form.has("dismissible");
      if (surfaces.includes("in_playback") && (!style || !dismissible))
        throw new Error("In-playback campaigns require a presentation style and dismissal.");
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
          <h2 className="text-xl font-semibold">{initial ? "Edit campaign" : "Create campaign"}</h2>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="headline"
              label="Headline"
              value={initial?.headline}
              required
              maxLength={120}
            />
            <Field name="kicker" label="Kicker" value={initial?.kicker} maxLength={40} />
            <Field name="subtitle" label="Subtitle" value={initial?.subtitle} maxLength={200} />
            <Field
              name="organization_id"
              label="Owning organization ID (blank for deployment-wide)"
              value={initial?.organization_id}
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
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="image_width"
              label="Image width (optional)"
              type="number"
              min={1}
              value={initial?.image_width}
            />
            <Field
              name="image_height"
              label="Image height (optional)"
              type="number"
              min={1}
              value={initial?.image_height}
            />
          </div>
          <p className="text-muted-foreground text-sm">
            Use 16:9 artwork. Declared dimensions are checked by Server.
          </p>
          <fieldset className="space-y-3">
            <legend className="font-medium">Placements</legend>
            <div className="flex flex-wrap gap-4">
              {["home", "detail", "pre_playback", "in_playback"].map((surface) => (
                <Check
                  key={surface}
                  name="surface"
                  value={surface}
                  label={surface.replaceAll("_", " ")}
                  checked={initial ? initial.surfaces.includes(surface) : surface === "home"}
                />
              ))}
            </div>
          </fieldset>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="home_position"
              label="Home row position (0 is first)"
              type="number"
              min={0}
              value={initial?.placement.home_position ?? 1}
            />
            <Field
              name="detail_slot"
              label="Detail slot hint (optional)"
              value={initial?.placement.detail_slot}
            />
            <Field
              name="content_ids"
              label="Restrict to content IDs (comma-separated, optional)"
              value={initial?.placement.content_ids?.join(", ")}
            />
            <Field name="priority" label="Priority" type="number" value={initial?.priority ?? 0} />
          </div>
          <label className="block space-y-2 text-sm">
            In-playback presentation
            <select
              name="playback_style"
              className={inputClass}
              defaultValue={initial?.placement.playback_style ?? ""}
            >
              <option value="">None</option>
              <option value="card">Card</option>
              <option value="pip">Muted picture-in-picture</option>
            </select>
          </label>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="duration_seconds"
              label="Overlay duration (5–60 seconds; never a forced wait)"
              type="number"
              min={5}
              max={60}
              value={initial?.placement.duration_seconds || 10}
            />
            <Field
              name="video_url"
              label="Muted PiP video URL (HTTPS, optional)"
              value={initial?.placement.video_url}
            />
          </div>
          <WindowFields start={initial?.starts_at} end={initial?.ends_at} />
          <fieldset className="space-y-4">
            <legend className="font-medium">Audience</legend>
            <label className="block space-y-2 text-sm">
              Audience type
              <select
                name="audience"
                className={inputClass}
                value={audience}
                onChange={(event) =>
                  setAudience(event.target.value as CampaignTargeting["audience"])
                }
              >
                {["all", "role", "organization", "library", "explicit"].map((value) => (
                  <option key={value} value={value}>
                    {value}
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
                  <option value="user">User</option>
                  <option value="admin">Administrator</option>
                </select>
              </label>
            )}
            {audience === "organization" && (
              <Field
                name="target_organization"
                label="Audience organization ID"
                value={initial?.targeting.organization_id}
                required
              />
            )}
            {audience === "library" && (
              <Field
                name="library_id"
                label="Audience library ID"
                type="number"
                min={1}
                value={initial?.targeting.library_id}
                required
              />
            )}
            {audience === "explicit" && (
              <div className="grid gap-4 sm:grid-cols-2">
                <Field
                  name="user_ids"
                  label="Account IDs (comma-separated)"
                  value={initial?.targeting.user_ids?.join(", ")}
                />
                <Field
                  name="profile_ids"
                  label="Profile IDs (comma-separated)"
                  value={initial?.targeting.profile_ids?.join(", ")}
                />
              </div>
            )}
          </fieldset>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field name="deeplink" label="Deep link (optional)" value={initial?.deeplink} />
            <Field
              name="cta_label"
              label="CTA label (optional)"
              value={initial?.cta?.label}
              maxLength={40}
            />
            <Field
              name="cta_url"
              label="CTA destination (HTTPS, app path or bloem://)"
              value={initial?.cta?.url}
            />
          </div>
          <Check
            name="dismissible"
            label="Allow viewers to dismiss"
            checked={initial?.dismissible ?? true}
          />
          <div className="flex gap-3">
            <Button type="submit">Review campaign</Button>
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
          <h3 className="font-semibold">Review before publishing</h3>
          <p className="text-muted-foreground text-sm">
            Audience: {review.targeting.audience}. Placements: {review.surfaces.join(", ")}.{" "}
            {review.starts_at} to {review.ends_at}.
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
          <h2 className="text-xl font-semibold">
            {initial ? "Edit seasonal pack" : "Create seasonal pack"}
          </h2>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="effect_id"
              label="Effect ID (snow, or an artwork-pack slug)"
              value={initial?.effect_id ?? "snow"}
              required
              maxLength={64}
            />
            <Field
              name="organization_id"
              label="Organization ID (blank for deployment-wide)"
              value={initial?.organization_id}
            />
            <Field
              name="intensity"
              label="Intensity (0 disables rendering; 1 is full)"
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
              <Field
                name="timezone"
                label="IANA timezone for annual repeats"
                value={initial?.window.timezone ?? "UTC"}
                required
              />
            </>
          )}
          <fieldset className="space-y-3">
            <legend className="font-medium">Surfaces (none means all)</legend>
            <div className="flex flex-wrap gap-4">
              {["all", "home", "login"].map((surface) => (
                <Check
                  key={surface}
                  name="surface"
                  value={surface}
                  label={surface}
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
            Sprite URLs (one per line, maximum 32)
            <textarea
              className="border-input bg-background min-h-24 w-full rounded-md border p-3"
              value={sprites}
              onChange={(event) => setSprites(event.target.value)}
            />
          </label>
          {storageAvailable && (
            <label className="block text-sm">
              Add a sprite image
              <input
                type="file"
                accept="image/png,image/jpeg,image/webp,image/gif"
                className="mt-2 block max-w-full text-sm"
                onChange={(event) => {
                  const file = event.currentTarget.files?.[0];
                  event.currentTarget.value = "";
                  if (!file) return;
                  if (file.size > 8 * 1024 * 1024) {
                    setError("Artwork must be at most 8 MiB.");
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
            <Button type="submit">Review seasonal pack</Button>
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
          <h3 className="font-semibold">Review before publishing</h3>
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
