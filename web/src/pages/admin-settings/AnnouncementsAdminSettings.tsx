import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Megaphone } from "lucide-react";
import { api } from "@/api/client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SettingsPageHeader } from "@/components/settings/SettingsPageHeader";
import { useReportUnsavedChanges } from "@/hooks/useUnsavedChanges";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

const endpoint = "/admin/notifications/announcements";
const queryKey = ["admin", "announcements"];
type Severity = "info" | "warning" | "critical";
type Audience = "all" | "role" | "organization" | "library" | "explicit";
interface AnnouncementBody {
  title: string;
  body: string;
  severity: Severity;
  dismissible: boolean;
  deeplink?: string;
  image_url?: string;
  expires_at?: string;
  cta?: { label: string; url: string };
}
interface Targeting {
  audience: Audience;
  role?: string;
  organization_id?: string;
  library_id?: number;
  user_ids?: number[];
  profile_ids?: string[];
}
interface AnnouncementInput {
  type: string;
  body: AnnouncementBody;
  targeting: Targeting;
}
interface Announcement extends AnnouncementInput {
  id: string;
  recipient_count: number;
  created_at: string;
  withdrawn_at: string | null;
}
const selectClass = "border-input bg-background h-10 w-full rounded-md border px-3 text-sm";
function errorText(error: unknown) {
  return error instanceof Error ? error.message : "The announcement could not be saved. Try again.";
}
function allowedLink(value: string, image = false) {
  if (!value.trim()) return true;
  try {
    const url = new URL(value);
    return (
      !url.username &&
      !url.password &&
      (url.protocol === "https:" || (!image && url.protocol === "bloem:"))
    );
  } catch {
    return false;
  }
}
function audienceSummary(target: Targeting) {
  switch (target.audience) {
    case "all":
      return "All viewers";
    case "role":
      return target.role === "admin" ? "Administrators" : "Users";
    case "organization":
      return `Organization ${target.organization_id}`;
    case "library":
      return `Viewers with access to library ${target.library_id}`;
    case "explicit":
      return [
        target.user_ids?.length ? `Users: ${target.user_ids.join(", ")} (all their profiles)` : "",
        target.profile_ids?.length ? `Profiles: ${target.profile_ids.join(", ")}` : "",
      ]
        .filter(Boolean)
        .join("; ");
  }
}
function status(item: Announcement) {
  if (item.withdrawn_at) return "Withdrawn";
  if (item.body.expires_at && Date.parse(item.body.expires_at) <= Date.now()) return "Expired";
  return "Published";
}

export default function AnnouncementsAdminSettings() {
  const cache = useQueryClient();
  const listing = useQuery({
    queryKey,
    queryFn: () => api<{ announcements: Announcement[] }>(endpoint),
  });
  const [title, setTitle] = useState("");
  const [message, setMessage] = useState("");
  const [severity, setSeverity] = useState<Severity>("info");
  const [audience, setAudience] = useState<Audience>("all");
  const [role, setRole] = useState("user");
  const [target, setTarget] = useState("");
  const [profiles, setProfiles] = useState("");
  const [dismissible, setDismissible] = useState(true);
  const [link, setLink] = useState("");
  const [image, setImage] = useState("");
  const [ctaLabel, setCtaLabel] = useState("");
  const [ctaUrl, setCtaUrl] = useState("");
  const [expiry, setExpiry] = useState("");
  const [validation, setValidation] = useState("");
  const [preview, setPreview] = useState<AnnouncementInput | null>(null);
  const [withdrawing, setWithdrawing] = useState<Announcement | null>(null);
  const [receipt, setReceipt] = useState("");
  useReportUnsavedChanges(
    Boolean(
      title ||
      message ||
      link ||
      image ||
      ctaLabel ||
      ctaUrl ||
      expiry ||
      target ||
      profiles ||
      severity !== "info" ||
      audience !== "all" ||
      role !== "user" ||
      !dismissible,
    ),
  );
  const publish = useMutation({
    mutationFn: (input: AnnouncementInput) =>
      api<Announcement>(endpoint, { method: "POST", body: JSON.stringify(input) }),
    onSuccess: (result) => {
      setReceipt(
        typeof result.recipient_count === "number"
          ? `Published to ${result.recipient_count} recipients.`
          : "Announcement published.",
      );
      setSeverity("info");
      setAudience("all");
      setRole("user");
      setDismissible(true);
      setTitle("");
      setMessage("");
      setLink("");
      setImage("");
      setCtaLabel("");
      setCtaUrl("");
      setExpiry("");
      setTarget("");
      setProfiles("");
      setPreview(null);
      void cache.invalidateQueries({ queryKey });
    },
  });
  const withdraw = useMutation({
    mutationFn: (id: string) => api(endpoint + "/" + encodeURIComponent(id), { method: "DELETE" }),
    onSuccess: () => {
      setWithdrawing(null);
      setReceipt("Announcement withdrawn.");
      void cache.invalidateQueries({ queryKey });
    },
  });
  function prepare(event: FormEvent) {
    event.preventDefault();
    setValidation("");
    publish.reset();
    if (!title.trim() || !message.trim()) {
      setValidation("Add a title and a message.");
      return;
    }
    if (!allowedLink(link) || !allowedLink(ctaUrl) || !allowedLink(image, true)) {
      setValidation("Use HTTPS links or Bloem app links. Images need an HTTPS address.");
      return;
    }
    if (Boolean(ctaLabel.trim()) !== Boolean(ctaUrl.trim())) {
      setValidation("Add both an action label and its link.");
      return;
    }
    if (expiry && (!Number.isFinite(Date.parse(expiry)) || Date.parse(expiry) <= Date.now())) {
      setValidation("Choose an expiry time in the future.");
      return;
    }
    const targeting: Targeting = { audience };
    if (audience === "role") targeting.role = role;
    if (audience === "organization") {
      if (!target.trim()) {
        setValidation("Enter the organization ID.");
        return;
      }
      targeting.organization_id = target.trim();
    }
    if (audience === "library") {
      if (
        !/^\d+$/.test(target.trim()) ||
        Number(target) <= 0 ||
        !Number.isSafeInteger(Number(target))
      ) {
        setValidation("Enter a valid library ID.");
        return;
      }
      targeting.library_id = Number(target);
    }
    if (audience === "explicit") {
      const userValues = target
        .split(",")
        .map((x) => x.trim())
        .filter(Boolean);
      const profileValues = profiles
        .split(",")
        .map((x) => x.trim())
        .filter(Boolean);
      if (
        userValues.some(
          (x) => !/^\d+$/.test(x) || Number(x) <= 0 || !Number.isSafeInteger(Number(x)),
        ) ||
        (!userValues.length && !profileValues.length)
      ) {
        setValidation("Choose at least one valid user or profile ID.");
        return;
      }
      targeting.user_ids = [...new Set(userValues.map(Number))];
      targeting.profile_ids = [...new Set(profileValues)];
    }
    setPreview({
      type: "system.announcement",
      targeting,
      body: {
        title: title.trim(),
        body: message.trim(),
        severity,
        dismissible: severity === "critical" ? false : dismissible,
        ...(link.trim() ? { deeplink: link.trim() } : {}),
        ...(image.trim() ? { image_url: image.trim() } : {}),
        ...(expiry ? { expires_at: new Date(expiry).toISOString() } : {}),
        ...(ctaLabel.trim() ? { cta: { label: ctaLabel.trim(), url: ctaUrl.trim() } } : {}),
      },
    });
  }
  return (
    <div className="space-y-8">
      <SettingsPageHeader title="Announcements" />
      <p className="text-muted-foreground text-sm">
        Publish messages to your viewers’ inboxes. Supporting clients can also show them as banners.
      </p>
      {receipt && (
        <p role="status" className="text-sm">
          {receipt}
        </p>
      )}
      <form onSubmit={prepare} className="space-y-4">
        <h2 className="text-lg font-semibold">New announcement</h2>
        <label className="block space-y-2 text-sm">
          Title
          <Input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            maxLength={120}
            required
          />
        </label>
        <label className="block space-y-2 text-sm">
          Message
          <textarea
            className="border-input bg-background w-full rounded-md border px-3 py-2 text-sm"
            value={message}
            onChange={(e) => setMessage(e.target.value)}
            maxLength={2000}
            rows={4}
            required
          />
        </label>
        <div className="grid gap-4 sm:grid-cols-2">
          <label className="block space-y-2 text-sm">
            Severity
            <select
              aria-label="Severity"
              className={selectClass}
              value={severity}
              onChange={(e) => setSeverity(e.target.value as Severity)}
            >
              <option value="info">Information</option>
              <option value="warning">Warning</option>
              <option value="critical">Critical</option>
            </select>
          </label>
          <label className="block space-y-2 text-sm">
            Audience
            <select
              aria-label="Audience"
              className={selectClass}
              value={audience}
              onChange={(e) => {
                setAudience(e.target.value as Audience);
                setTarget("");
                setProfiles("");
              }}
            >
              <option value="all">All viewers</option>
              <option value="role">Account role</option>
              <option value="organization">Organization</option>
              <option value="library">Viewers with library access</option>
              <option value="explicit">Specific users or profiles</option>
            </select>
          </label>
        </div>
        {audience === "role" && (
          <label className="block space-y-2 text-sm">
            Account role
            <select className={selectClass} value={role} onChange={(e) => setRole(e.target.value)}>
              <option value="user">Users</option>
              <option value="admin">Administrators</option>
            </select>
          </label>
        )}
        {["organization", "library", "explicit"].includes(audience) && (
          <label className="block space-y-2 text-sm">
            {audience === "organization"
              ? "Organization ID"
              : audience === "library"
                ? "Library ID"
                : "User IDs, separated by commas"}
            <Input value={target} onChange={(e) => setTarget(e.target.value)} />
          </label>
        )}
        {audience === "explicit" && (
          <label className="block space-y-2 text-sm">
            Profile IDs, separated by commas
            <Input value={profiles} onChange={(e) => setProfiles(e.target.value)} />
          </label>
        )}
        <label className="flex items-center gap-3 text-sm">
          <input
            type="checkbox"
            checked={severity !== "critical" && dismissible}
            disabled={severity === "critical"}
            onChange={(e) => setDismissible(e.target.checked)}
          />
          Allow viewers to dismiss
        </label>
        {severity === "critical" && (
          <p className="text-muted-foreground text-sm">
            Viewers cannot dismiss critical announcements.
          </p>
        )}
        <details className="rounded-lg border p-4">
          <summary className="cursor-pointer text-sm font-medium">
            Links, artwork and expiry
          </summary>
          <div className="mt-4 space-y-4">
            <label className="block space-y-2 text-sm">
              App or web link
              <Input value={link} onChange={(e) => setLink(e.target.value)} maxLength={2048} />
            </label>
            <label className="block space-y-2 text-sm">
              Image URL
              <Input value={image} onChange={(e) => setImage(e.target.value)} maxLength={2048} />
            </label>
            <label className="block space-y-2 text-sm">
              Action label
              <Input
                value={ctaLabel}
                onChange={(e) => setCtaLabel(e.target.value)}
                maxLength={40}
              />
            </label>
            <label className="block space-y-2 text-sm">
              Action link
              <Input value={ctaUrl} onChange={(e) => setCtaUrl(e.target.value)} maxLength={2048} />
            </label>
            <label className="block space-y-2 text-sm">
              Expires at (your local time)
              <Input
                type="datetime-local"
                value={expiry}
                onChange={(e) => setExpiry(e.target.value)}
              />
            </label>
          </div>
        </details>
        {validation && (
          <p role="alert" className="text-destructive text-sm">
            {validation}
          </p>
        )}
        <Button type="submit">Preview announcement</Button>
      </form>
      <section className="space-y-4" aria-label="Published announcements">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-lg font-semibold">Published announcements</h2>
          <Button
            variant="outline"
            onClick={() => void listing.refetch()}
            disabled={listing.isFetching}
          >
            Refresh
          </Button>
        </div>
        {listing.isLoading && <p role="status">Loading announcements…</p>}
        {listing.error && (
          <p role="alert" className="text-destructive">
            {errorText(listing.error)}
          </p>
        )}
        {listing.data?.announcements.length === 0 && (
          <p className="text-muted-foreground text-sm">No announcements have been published.</p>
        )}
        {listing.data?.announcements.map((item) => (
          <article key={item.id} className="space-y-3 rounded-xl border p-4">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <h3 className="font-semibold">{item.body.title}</h3>
              <span className="text-muted-foreground text-sm">{status(item)}</span>
            </div>
            <p className="text-sm whitespace-pre-wrap">{item.body.body}</p>
            <p className="text-muted-foreground text-sm">
              {item.body.severity} · {item.recipient_count} recipients ·{" "}
              {new Date(item.created_at).toLocaleString()}
            </p>
            {!item.withdrawn_at && status(item) !== "Expired" && (
              <Button
                variant="outline"
                aria-label={`Withdraw ${item.body.title}`}
                onClick={() => {
                  withdraw.reset();
                  setWithdrawing(item);
                }}
              >
                Withdraw
              </Button>
            )}
          </article>
        ))}
      </section>
      <Dialog
        open={Boolean(preview)}
        onOpenChange={(open) => {
          if (!open && !publish.isPending) setPreview(null);
        }}
      >
        <DialogContent className="max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>Preview announcement</DialogTitle>
            <DialogDescription>
              Publishing sends this message immediately to{" "}
              {preview ? audienceSummary(preview.targeting) : "the selected audience"}. Published
              messages can be withdrawn.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3 rounded-lg border p-4">
            <Megaphone className="h-5 w-5" aria-hidden="true" />
            <p className="text-muted-foreground text-xs uppercase">{preview?.body.severity}</p>
            <h3 className="font-semibold">{preview?.body.title}</h3>
            <p className="text-sm whitespace-pre-wrap">{preview?.body.body}</p>
            {preview?.body.cta && (
              <span className="text-sm font-medium">{preview.body.cta.label}</span>
            )}
          </div>
          {preview && (
            <div className="text-muted-foreground space-y-2 text-sm break-words">
              <p>
                {preview.body.dismissible
                  ? "Viewers can dismiss this message."
                  : "Viewers cannot dismiss this message."}
              </p>
              <p>
                Expires:{" "}
                {preview.body.expires_at
                  ? new Date(preview.body.expires_at).toLocaleString()
                  : "No expiry"}
              </p>
              {preview.body.deeplink && <p>Link: {preview.body.deeplink}</p>}
              {preview.body.image_url && <p>Artwork: {preview.body.image_url}</p>}
              {preview.body.cta && <p>Action link: {preview.body.cta.url}</p>}
            </div>
          )}
          {publish.error && (
            <p role="alert" className="text-destructive text-sm">
              {errorText(publish.error)}
            </p>
          )}
          <DialogFooter>
            <Button variant="outline" disabled={publish.isPending} onClick={() => setPreview(null)}>
              Keep editing
            </Button>
            <Button
              disabled={publish.isPending}
              onClick={() => {
                if (preview && !publish.isPending) publish.mutate(preview);
              }}
            >
              {publish.isPending ? "Publishing…" : "Publish announcement"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog
        open={Boolean(withdrawing)}
        onOpenChange={(open) => {
          if (!open && !withdraw.isPending) setWithdrawing(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Withdraw announcement?</DialogTitle>
            <DialogDescription>
              Remove “{withdrawing?.body.title}” from delivery. Its publication record will remain
              here.
            </DialogDescription>
          </DialogHeader>
          {withdraw.error && (
            <p role="alert" className="text-destructive">
              {errorText(withdraw.error)}
            </p>
          )}
          <DialogFooter>
            <Button
              variant="outline"
              disabled={withdraw.isPending}
              onClick={() => setWithdrawing(null)}
            >
              Cancel
            </Button>
            <Button
              disabled={withdraw.isPending}
              onClick={() => {
                if (withdrawing && !withdraw.isPending) withdraw.mutate(withdrawing.id);
              }}
            >
              {withdraw.isPending ? "Withdrawing…" : "Withdraw announcement"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
