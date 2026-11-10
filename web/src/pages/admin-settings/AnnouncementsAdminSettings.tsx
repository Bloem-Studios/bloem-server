import { bloemT, useBloemT } from "@/bloem/i18n";
import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Megaphone } from "lucide-react";
import { adminV2Api, adminV2QueryKey, captureAdminRequestContext } from "@/api/adminV2Client";
import type { AdminContextSummary } from "@/api/bloemTypes";
import { useOptionalAdminContext } from "@/contexts/AdminContextProvider";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { AccountPicker } from "@/components/admin/policy/AccountPicker";
import { useAdminLibraries } from "@/hooks/queries/admin/libraries";
import { useOrganizationLibraries } from "@/hooks/queries/admin/organizationLibraries";
import { usePlatformOrganizations } from "@/hooks/queries/admin/organizations";
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

// Announcements reach the whole server, so they are platform administration.
const endpoint = "/platform/announcements";
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
  return error instanceof Error ? error.message : bloemT("bloem.admin.announcements.couldNotSave");
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
      return bloemT("bloem.admin.announcements.audienceAll");
    case "role":
      return target.role === "admin"
        ? bloemT("bloem.admin.announcements.audienceAdministrators")
        : bloemT("bloem.admin.announcements.audienceUsers");
    case "organization":
      return bloemT("bloem.admin.announcements.audienceOrganization", {
        organization: target.organization_id ?? "",
      });
    case "library":
      return bloemT("bloem.admin.announcements.audienceLibrary", {
        library: target.library_id ?? "",
      });
    case "explicit":
      return [
        target.user_ids?.length
          ? bloemT("bloem.admin.announcements.audienceAccounts", {
              accounts: target.user_ids.join(", "),
            })
          : "",
        target.profile_ids?.length
          ? bloemT("bloem.admin.announcements.audienceProfiles", {
              profiles: target.profile_ids.join(", "),
            })
          : "",
      ]
        .filter(Boolean)
        .join("; ");
  }
}
type Status = "withdrawn" | "expired" | "published";
function status(item: Announcement): Status {
  if (item.withdrawn_at) return "withdrawn";
  if (item.body.expires_at && Date.parse(item.body.expires_at) <= Date.now()) return "expired";
  return "published";
}

export default function AnnouncementsAdminSettings() {
  const bt = useBloemT();
  // Optional: the settings layout can render outside AdminContextProvider.
  const active = useOptionalAdminContext()?.active ?? null;
  if (active?.scope !== "platform")
    return <p role="alert">{bt("bloem.admin.announcements.selectThePlatformContextTo")}</p>;
  return (
    <AnnouncementsComposer
      key={`${active.key}:${captureAdminRequestContext(active.key)?.generation ?? "unbound"}`}
      context={active}
    />
  );
}

function AnnouncementsComposer({ context }: { context: AdminContextSummary }) {
  const bt = useBloemT();
  const cache = useQueryClient();
  const authority = captureAdminRequestContext(context.key);
  const queryKey = adminV2QueryKey(context.key, "announcements", authority?.generation);
  const listing = useQuery({
    queryKey,
    queryFn: ({ signal }) =>
      adminV2Api<{ announcements: Announcement[] }>(endpoint, { signal }, "safe", authority),
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
      adminV2Api<Announcement>(
        endpoint,
        { method: "POST", body: JSON.stringify(input) },
        "none",
        authority,
      ),
    onSuccess: (result) => {
      setReceipt(
        typeof result.recipient_count === "number"
          ? bt("bloem.admin.announcements.publishedTo", { count: result.recipient_count })
          : bt("bloem.admin.announcements.publishedReceipt"),
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
    mutationFn: (id: string) =>
      adminV2Api(endpoint + "/" + encodeURIComponent(id), { method: "DELETE" }, "none", authority),
    onSuccess: () => {
      setWithdrawing(null);
      setReceipt(bt("bloem.admin.announcements.withdrawnReceipt"));
      void cache.invalidateQueries({ queryKey });
    },
  });
  function prepare(event: FormEvent) {
    event.preventDefault();
    setValidation("");
    publish.reset();
    if (!title.trim() || !message.trim()) {
      setValidation(bt("bloem.admin.announcements.needTitleAndMessage"));
      return;
    }
    if (!allowedLink(link) || !allowedLink(ctaUrl) || !allowedLink(image, true)) {
      setValidation(bt("bloem.admin.announcements.needHttpsLinks"));
      return;
    }
    if (Boolean(ctaLabel.trim()) !== Boolean(ctaUrl.trim())) {
      setValidation(bt("bloem.admin.announcements.needActionLabelAndLink"));
      return;
    }
    if (expiry && (!Number.isFinite(Date.parse(expiry)) || Date.parse(expiry) <= Date.now())) {
      setValidation(bt("bloem.admin.announcements.needFutureExpiry"));
      return;
    }
    const targeting: Targeting = { audience };
    if (audience === "role") targeting.role = role;
    if (audience === "organization") {
      if (!target.trim()) {
        setValidation(bt("bloem.admin.announcements.needOrganization"));
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
        setValidation(bt("bloem.admin.announcements.needLibrary"));
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
        setValidation(bt("bloem.admin.announcements.needAccountsOrProfiles"));
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
      <SettingsPageHeader title={bt("bloem.admin.announcements.announcements")} />
      <p className="text-muted-foreground text-sm">
        {bt("bloem.admin.announcements.publishMessagesToYourViewers")}
      </p>
      {receipt && (
        <p role="status" className="text-sm">
          {receipt}
        </p>
      )}
      <form onSubmit={prepare} className="space-y-4">
        <h2 className="text-lg font-semibold">{bt("bloem.admin.announcements.newAnnouncement")}</h2>
        <label className="block space-y-2 text-sm">
          {bt("bloem.admin.announcements.title")}
          <Input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            maxLength={120}
            required
          />
        </label>
        <label className="block space-y-2 text-sm">
          {bt("bloem.admin.announcements.message")}
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
            {bt("bloem.admin.announcements.severity")}
            <select
              aria-label={bt("bloem.admin.announcements.severity")}
              className={selectClass}
              value={severity}
              onChange={(e) => setSeverity(e.target.value as Severity)}
            >
              <option value="info">{bt("bloem.admin.announcements.information")}</option>
              <option value="warning">{bt("bloem.admin.announcements.warning")}</option>
              <option value="critical">{bt("bloem.admin.announcements.critical")}</option>
            </select>
          </label>
          <label className="block space-y-2 text-sm">
            {bt("bloem.admin.announcements.audience")}
            <select
              aria-label={bt("bloem.admin.announcements.audience")}
              className={selectClass}
              value={audience}
              onChange={(e) => {
                setAudience(e.target.value as Audience);
                setTarget("");
                setProfiles("");
              }}
            >
              <option value="all">{bt("bloem.admin.announcements.allViewers")}</option>
              <option value="role">{bt("bloem.admin.announcements.accountRole")}</option>
              <option value="organization">{bt("bloem.admin.announcements.organization")}</option>
              <option value="library">
                {bt("bloem.admin.announcements.viewersWithLibraryAccess")}
              </option>
              <option value="explicit">
                {bt("bloem.admin.announcements.specificUsersOrProfiles")}
              </option>
            </select>
          </label>
        </div>
        {audience === "role" && (
          <label className="block space-y-2 text-sm">
            {bt("bloem.admin.announcements.accountRole")}
            <select className={selectClass} value={role} onChange={(e) => setRole(e.target.value)}>
              <option value="user">{bt("bloem.admin.announcements.users")}</option>
              <option value="admin">{bt("bloem.admin.announcements.administrators")}</option>
            </select>
          </label>
        )}
        {audience === "organization" && (
          <OrganizationTarget context={context} value={target} onChange={setTarget} />
        )}
        {audience === "library" && (
          <LibraryTarget context={context} value={target} onChange={setTarget} />
        )}
        {audience === "explicit" && (
          <>
            <AccountPicker
              id="announcement-accounts"
              label={bt("bloem.admin.announcements.accountsAllTheirProfiles")}
              selected={accountNumbers(target)}
              onChange={(ids) => setTarget(ids.join(", "))}
            />
            <label className="block space-y-2 text-sm">
              {bt("bloem.admin.announcements.orAccountNumbersSeparatedBy")}
              <Input value={target} onChange={(e) => setTarget(e.target.value)} />
            </label>
            <label className="block space-y-2 text-sm">
              {bt("bloem.admin.announcements.profileIdsSeparatedByCommas")}
              <Input value={profiles} onChange={(e) => setProfiles(e.target.value)} />
            </label>
          </>
        )}
        <label className="flex items-center gap-3 text-sm">
          <input
            type="checkbox"
            checked={severity !== "critical" && dismissible}
            disabled={severity === "critical"}
            onChange={(e) => setDismissible(e.target.checked)}
          />
          {bt("bloem.admin.announcements.allowViewersToDismiss")}
        </label>
        {severity === "critical" && (
          <p className="text-muted-foreground text-sm">
            {bt("bloem.admin.announcements.viewersCannotDismissCriticalAnnouncements")}
          </p>
        )}
        <details className="rounded-lg border p-4">
          <summary className="cursor-pointer text-sm font-medium">
            {bt("bloem.admin.announcements.linksArtworkAndExpiry")}
          </summary>
          <div className="mt-4 space-y-4">
            <label className="block space-y-2 text-sm">
              {bt("bloem.admin.announcements.appOrWebLink")}
              <Input value={link} onChange={(e) => setLink(e.target.value)} maxLength={2048} />
            </label>
            <label className="block space-y-2 text-sm">
              {bt("bloem.admin.announcements.imageUrl")}
              <Input value={image} onChange={(e) => setImage(e.target.value)} maxLength={2048} />
            </label>
            <label className="block space-y-2 text-sm">
              {bt("bloem.admin.announcements.actionLabel")}
              <Input
                value={ctaLabel}
                onChange={(e) => setCtaLabel(e.target.value)}
                maxLength={40}
              />
            </label>
            <label className="block space-y-2 text-sm">
              {bt("bloem.admin.announcements.actionLink")}
              <Input value={ctaUrl} onChange={(e) => setCtaUrl(e.target.value)} maxLength={2048} />
            </label>
            <label className="block space-y-2 text-sm">
              {bt("bloem.admin.announcements.expiresAtYourLocalTime")}
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
        <Button type="submit">{bt("bloem.admin.announcements.previewAnnouncement")}</Button>
      </form>
      <section
        className="space-y-4"
        aria-label={bt("bloem.admin.announcements.publishedAnnouncements")}
      >
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-lg font-semibold">
            {bt("bloem.admin.announcements.publishedAnnouncements")}
          </h2>
          <Button
            variant="outline"
            onClick={() => void listing.refetch()}
            disabled={listing.isFetching}
          >
            {bt("bloem.admin.announcements.refresh")}
          </Button>
        </div>
        {listing.isLoading && (
          <p role="status">{bt("bloem.admin.announcements.loadingAnnouncements")}</p>
        )}
        {listing.error && (
          <p role="alert" className="text-destructive">
            {errorText(listing.error)}
          </p>
        )}
        {listing.data?.announcements.length === 0 && (
          <p className="text-muted-foreground text-sm">
            {bt("bloem.admin.announcements.noAnnouncementsHaveBeenPublished")}
          </p>
        )}
        {listing.data?.announcements.map((item) => (
          <article key={item.id} className="space-y-3 rounded-xl border p-4">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <h3 className="font-semibold">{item.body.title}</h3>
              <span className="text-muted-foreground text-sm">
                {bt("bloem.admin.announcements.status", { status: status(item) })}
              </span>
            </div>
            <p className="text-sm whitespace-pre-wrap">{item.body.body}</p>
            <p className="text-muted-foreground text-sm">
              {bt("bloem.admin.announcements.recipients", {
                severity: bt("bloem.admin.announcements.severityName", {
                  severity: item.body.severity,
                }),
                recipientCount: item.recipient_count,
                created: new Date(item.created_at).toLocaleString(),
              })}
            </p>
            {!item.withdrawn_at && status(item) !== "expired" && (
              <Button
                variant="outline"
                aria-label={bt("bloem.admin.announcements.withdraw", { title: item.body.title })}
                onClick={() => {
                  withdraw.reset();
                  setWithdrawing(item);
                }}
              >
                {bt("bloem.admin.announcements.withdraw2")}
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
            <DialogTitle>{bt("bloem.admin.announcements.previewAnnouncement")}</DialogTitle>
            <DialogDescription>
              {bt("bloem.admin.announcements.publishingSendsTo", {
                audience: preview
                  ? audienceSummary(preview.targeting)
                  : bt("bloem.admin.announcements.theSelectedAudience"),
              })}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3 rounded-lg border p-4">
            <Megaphone className="h-5 w-5" aria-hidden="true" />
            <p className="text-muted-foreground text-xs uppercase">
              {preview
                ? bt("bloem.admin.announcements.severityName", {
                    severity: preview.body.severity,
                  })
                : null}
            </p>
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
                  ? bt("bloem.admin.announcements.viewersCanDismissThisMessage")
                  : bt("bloem.admin.announcements.viewersCannotDismissThisMessage")}
              </p>
              <p>
                {bt("bloem.admin.announcements.expires", {
                  expiry: preview.body.expires_at
                    ? new Date(preview.body.expires_at).toLocaleString()
                    : bt("bloem.admin.announcements.noExpiry"),
                })}
              </p>
              {preview.body.deeplink && (
                <p>{bt("bloem.admin.announcements.link", { deeplink: preview.body.deeplink })}</p>
              )}
              {preview.body.image_url && (
                <p>
                  {bt("bloem.admin.announcements.artwork", { imageUrl: preview.body.image_url })}
                </p>
              )}
              {preview.body.cta && (
                <p>{bt("bloem.admin.announcements.actionLink2", { url: preview.body.cta.url })}</p>
              )}
            </div>
          )}
          {publish.error && (
            <p role="alert" className="text-destructive text-sm">
              {errorText(publish.error)}
            </p>
          )}
          <DialogFooter>
            <Button variant="outline" disabled={publish.isPending} onClick={() => setPreview(null)}>
              {bt("bloem.admin.announcements.keepEditing")}
            </Button>
            <Button
              disabled={publish.isPending}
              onClick={() => {
                if (preview && !publish.isPending) publish.mutate(preview);
              }}
            >
              {publish.isPending
                ? bt("bloem.admin.announcements.publishing")
                : bt("bloem.admin.announcements.publishAnnouncement")}
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
            <DialogTitle>{bt("bloem.admin.announcements.withdrawAnnouncement")}</DialogTitle>
            <DialogDescription>
              {bt("bloem.admin.announcements.removeFromDeliveryItsPublication", {
                title: withdrawing?.body.title,
              })}
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
              {bt("bloem.admin.announcements.cancel")}
            </Button>
            <Button
              disabled={withdraw.isPending}
              onClick={() => {
                if (withdrawing && !withdraw.isPending) withdraw.mutate(withdrawing.id);
              }}
            >
              {withdraw.isPending
                ? bt("bloem.admin.announcements.withdrawing")
                : bt("bloem.admin.announcements.withdrawAnnouncement2")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function accountNumbers(value: string): number[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter((item) => /^[1-9]\d*$/.test(item))
    .map(Number);
}

function OrganizationTarget({
  context,
  value,
  onChange,
}: {
  context: AdminContextSummary;
  value: string;
  onChange(value: string): void;
}) {
  const bt = useBloemT();
  const organizations = usePlatformOrganizations({ limit: 100 });
  const options = context.scope === "platform" ? (organizations.data?.organizations ?? []) : [];
  if (!options.length) {
    return (
      <label className="block space-y-2 text-sm">
        {bt("bloem.admin.announcements.organizationId")}
        <Input value={value} onChange={(e) => onChange(e.target.value)} />
      </label>
    );
  }
  return (
    <label className="block space-y-2 text-sm">
      {bt("bloem.admin.announcements.organization")}
      <select className={selectClass} value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">{bt("bloem.admin.announcements.chooseAnOrganization")}</option>
        {options.map((organization) => (
          <option key={organization.id} value={organization.id}>
            {organization.name}
          </option>
        ))}
      </select>
    </label>
  );
}

function LibraryTarget({
  context,
  value,
  onChange,
}: {
  context: AdminContextSummary;
  value: string;
  onChange(value: string): void;
}) {
  const bt = useBloemT();
  const platform = useAdminLibraries({ enabled: context.scope === "platform" });
  const organization = useOrganizationLibraries(context.key, context.scope === "organization");
  const options =
    context.scope === "platform"
      ? (platform.data ?? []).map(({ id, name }) => ({ id, name }))
      : (organization.data ?? []).map((library) => ({ id: library.folder_id, name: library.name }));
  if (!options.length) {
    return (
      <label className="block space-y-2 text-sm">
        {bt("bloem.admin.announcements.libraryId")}
        <Input value={value} onChange={(e) => onChange(e.target.value)} />
      </label>
    );
  }
  return (
    <label className="block space-y-2 text-sm">
      {bt("bloem.admin.announcements.library")}
      <select className={selectClass} value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">{bt("bloem.admin.announcements.chooseALibrary")}</option>
        {options.map((library) => (
          <option key={library.id} value={String(library.id)}>
            {library.name}
          </option>
        ))}
      </select>
    </label>
  );
}
