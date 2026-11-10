import { useBloemT, bloemT, type BloemMessageKey } from "@/bloem/i18n";
import { useMemo, useState } from "react";
import { captureAdminRequestContext } from "@/api/adminV2Client";
import type { FormEvent } from "react";
import type {
  Invitation,
  InvitationStatus,
  SendInvitationResponse,
} from "@/api/organizationInvitations";
import {
  useAdminInvitations,
  useCreateInvitation,
  useResendInvitation,
  useRevokeInvitation,
} from "@/hooks/queries/admin/organizationInvitations";
import { useAccessGroups } from "@/hooks/queries/admin/organizationAccessGroups";
import { useAdminLibraries } from "@/hooks/queries/admin/libraries";
import { effectiveAccessGroupID } from "@/components/UserPolicyFields";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { LibraryAccessScope } from "@/bloem/libraryAccessScope";
import { LibraryAccessSelector } from "@/components/LibraryAccessSelector";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { Copy, MailPlus, RotateCw, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { formatDate } from "@/lib/datetime";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import InvitationsTab from "./InvitationsTab";
import { useOrganizationLibraries } from "@/hooks/queries/admin/organizationLibraries";
import type { AdminContextSummary } from "@/api/bloemTypes";
import { useBloemCapabilities } from "@/hooks/queries/useBloemCapabilities";

// The claim-link box shown after create/resend. min-w-0 + overflow-hidden on
// every level matters: the URL is one unbreakable token, and without them it
// forces the dialog wider than the viewport on phones.
function ClaimLinkBox({
  claimUrl,
  finePrint,
  onCopy,
  onDone,
}: {
  claimUrl: string;
  finePrint: string;
  onCopy: (text: string) => void;
  onDone: () => void;
}) {
  const bt = useBloemT();
  return (
    <div className="min-w-0 space-y-4">
      <div className="bg-muted min-w-0 overflow-hidden rounded-md p-2.5">
        <code className="block truncate text-xs">{claimUrl}</code>
      </div>
      <p className="text-muted-foreground text-xs">{finePrint}</p>
      <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
        <Button variant="outline" onClick={() => onCopy(claimUrl)}>
          <Copy className="me-1.5 h-4 w-4" /> {bt("bloem.admin.orgInvitations.copyLink")}
        </Button>
        <Button onClick={onDone}>{bt("bloem.admin.orgInvitations.done")}</Button>
      </div>
    </div>
  );
}

const STATUS_BADGES: Record<
  InvitationStatus,
  { label: BloemMessageKey; variant: "default" | "outline" }
> = {
  pending: { label: "bloem.admin.orgInvitations.statusPending", variant: "default" },
  accepted: { label: "bloem.admin.orgInvitations.statusAccepted", variant: "outline" },
  expired: { label: "bloem.admin.orgInvitations.statusExpired", variant: "outline" },
  revoked: { label: "bloem.admin.orgInvitations.statusRevoked", variant: "outline" },
};

export default function OrganizationInvitationsTab() {
  const { active } = useAdminContext();
  // Outside an organization, invitations are Silo's own, on Silo's /api/v2.
  if (active?.scope !== "organization") return <InvitationsTab />;
  const generation = active ? captureAdminRequestContext(active.key)?.generation : undefined;
  return (
    <InvitationContent
      key={`${active?.key ?? "unavailable"}:${generation ?? "unbound"}`}
      active={active}
    />
  );
}

function InvitationContent({ active }: { active: AdminContextSummary | null }) {
  const bt = useBloemT();
  const organization = active?.scope === "organization";
  const {
    data: invitations = [],
    isLoading,
    isError,
    error,
    refetch,
  } = useAdminInvitations(active);
  const capabilities = useBloemCapabilities(organization);
  const lifecycleAvailable =
    !organization ||
    Boolean(capabilities.data?.feature_tokens?.includes("organization_invitation_lifecycle_v1"));
  const resend = useResendInvitation(active);
  const revoke = useRevokeInvitation(active);
  const [createOpen, setCreateOpen] = useState(false);
  const [confirmRevoke, setConfirmRevoke] = useState<Invitation | null>(null);
  const [confirmResend, setConfirmResend] = useState<Invitation | null>(null);
  // A resend mints a fresh single-use link; the response is the only chance
  // to read it, so we offer it for copying right away.
  const [resendResult, setResendResult] = useState<SendInvitationResponse | null>(null);

  function handleCopy(text: string) {
    void navigator.clipboard.writeText(text).then(
      () => toast.success(bloemT("bloem.admin.orgInvitations.copiedToClipboard")),
      () => toast.error(bloemT("bloem.admin.orgInvitations.couldNotCopyTheLink")),
    );
  }

  async function handleResend(id: number) {
    const data = await resend.send(id);
    if (data?.email_sent)
      toast.success(bloemT("bloem.admin.orgInvitations.invitationResentTheOldLink"));
    if (data?.claim_url) setResendResult(data);
  }

  if (isLoading) return <div>{bt("bloem.admin.orgInvitations.loadingInvitations")}</div>;
  if (isError)
    return (
      <div role="alert">
        <p>{error.message}</p>
        <Button onClick={() => void refetch()}>
          {bt("bloem.admin.orgInvitations.reloadInvitations")}
        </Button>
      </div>
    );

  return (
    <div className="space-y-6">
      {organization && (
        <div className="space-y-2">
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
            {bt("bloem.admin.orgInvitations.invitations")}
          </h1>
          <p className="page-subtitle">
            {bt("bloem.admin.orgInvitations.invitationsShownHereBelongOnly", { name: active.name })}
          </p>
        </div>
      )}
      <ConfirmDialog
        open={confirmResend !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmResend(null);
        }}
        title={bt("bloem.admin.orgInvitations.regenerateInvitationLink")}
        description={bt("bloem.admin.orgInvitations.thePreviousLinkWillStop")}
        confirmLabel={bt("bloem.admin.orgInvitations.regenerateLink")}
        isPending={resend.isPending}
        onConfirm={() => {
          if (confirmResend) void handleResend(confirmResend.id);
          setConfirmResend(null);
        }}
      />
      <ConfirmDialog
        open={confirmRevoke !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmRevoke(null);
        }}
        title={bt("bloem.admin.orgInvitations.revokeInvitation")}
        description={bt("bloem.admin.orgInvitations.revokeTheInvitationForTheir", {
          email: confirmRevoke?.email,
        })}
        confirmLabel={bt("bloem.admin.orgInvitations.revoke")}
        variant="destructive"
        onConfirm={() => {
          if (confirmRevoke) revoke.mutate(confirmRevoke.id);
          setConfirmRevoke(null);
        }}
      />

      <Dialog
        open={resendResult !== null}
        onOpenChange={(open) => {
          if (!open) setResendResult(null);
        }}
      >
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{bt("bloem.admin.orgInvitations.freshInvitationLink")}</DialogTitle>
            <DialogDescription>
              {resendResult?.email_sent
                ? bt("bloem.admin.orgInvitations.emailedToYouCanAlso", {
                    email: resendResult.invitation.email,
                  })
                : bt("bloem.admin.orgInvitations.noEmailWasSentDeliver")}
            </DialogDescription>
          </DialogHeader>
          {resendResult?.claim_url && (
            <ClaimLinkBox
              claimUrl={resendResult.claim_url}
              finePrint={bt("bloem.admin.orgInvitations.resentLinkFinePrint")}
              onCopy={handleCopy}
              onDone={() => setResendResult(null)}
            />
          )}
        </DialogContent>
      </Dialog>

      <div className="flex items-start justify-between gap-4">
        <p className="text-muted-foreground max-w-xl text-sm">
          {organization
            ? bt("bloem.admin.orgInvitations.createAPersonalInvitationLink")
            : bt("bloem.admin.orgInvitations.emailSomeoneAPersonalLink")}
        </p>
        <Dialog open={createOpen} onOpenChange={setCreateOpen}>
          <DialogTrigger asChild>
            <Button size="sm">
              <MailPlus className="me-1 h-4 w-4" /> {bt("bloem.admin.orgInvitations.inviteSomeone")}
            </Button>
          </DialogTrigger>
          <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
            <DialogHeader>
              <DialogTitle>{bt("bloem.admin.orgInvitations.inviteSomeone")}</DialogTitle>
              <DialogDescription>
                {organization
                  ? bt("bloem.admin.orgInvitations.createAOneTimeLink")
                  : bt("bloem.admin.orgInvitations.theyGetAnEmailWith")}
              </DialogDescription>
            </DialogHeader>
            {createOpen && (
              <CreateInvitationForm
                context={active}
                onClose={() => setCreateOpen(false)}
                onCopy={handleCopy}
              />
            )}
          </DialogContent>
        </Dialog>
      </div>

      {invitations.length === 0 ? (
        <p className="text-muted-foreground py-8 text-center text-sm">
          {bt("bloem.admin.orgInvitations.noInvitationsYetInviteSomeone")}
        </p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{bt("bloem.admin.orgInvitations.recipient")}</TableHead>
              <TableHead>{bt("bloem.admin.orgInvitations.role")}</TableHead>
              <TableHead>{bt("bloem.admin.orgInvitations.status")}</TableHead>
              <TableHead>{bt("bloem.admin.orgInvitations.sent")}</TableHead>
              <TableHead className="w-24" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {invitations.map((inv) => (
              <InvitationRow
                key={inv.id}
                invitation={inv}
                onResend={() => (organization ? setConfirmResend(inv) : void handleResend(inv.id))}
                onRevoke={() => setConfirmRevoke(inv)}
                resending={resend.isPending || revoke.isPending}
                allowActions={lifecycleAvailable && (!organization || inv.role === "user")}
              />
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}

function InvitationRow({
  invitation,
  onResend,
  onRevoke,
  resending,
  allowActions,
}: {
  invitation: Invitation;
  onResend: () => void;
  onRevoke: () => void;
  resending: boolean;
  allowActions: boolean;
}) {
  const bt = useBloemT();
  const badge = STATUS_BADGES[invitation.status];
  const showResend =
    allowActions && (invitation.status === "pending" || invitation.status === "expired");
  const showRevoke = allowActions && invitation.status === "pending";

  return (
    <TableRow>
      <TableCell>
        <div className="font-medium">{invitation.email}</div>
        {invitation.invited_by_name && (
          <div className="text-muted-foreground text-xs">
            {bt("bloem.admin.orgInvitations.invitedBy", {
              invitedByName: invitation.invited_by_name,
            })}
          </div>
        )}
      </TableCell>
      <TableCell className="text-muted-foreground capitalize">
        {bt("bloem.admin.orgInvitations.roleName", { role: invitation.role })}
      </TableCell>
      <TableCell>
        <Badge variant={badge.variant}>{bt(badge.label)}</Badge>
        {invitation.status === "pending" && (
          <span className="text-muted-foreground ms-2 text-xs">
            {bt("bloem.admin.orgInvitations.expires", { date: formatDate(invitation.expires_at) })}
          </span>
        )}
      </TableCell>
      <TableCell className="text-muted-foreground text-sm">
        {formatDate(invitation.created_at)}
      </TableCell>
      <TableCell>
        <div className="flex justify-end gap-1">
          {showResend && (
            <Button
              variant="ghost"
              size="sm"
              onClick={onResend}
              disabled={resending}
              title={bt("bloem.admin.orgInvitations.resendWithAFreshLink")}
            >
              <RotateCw className="h-4 w-4" />
            </Button>
          )}
          {showRevoke && (
            <Button
              variant="ghost"
              size="sm"
              onClick={onRevoke}
              title={bt("bloem.admin.orgInvitations.revokeThisLink")}
            >
              <Trash2 className="h-4 w-4" />
            </Button>
          )}
        </div>
      </TableCell>
    </TableRow>
  );
}

function CreateInvitationForm({
  context,
  onClose,
  onCopy,
}: {
  context: AdminContextSummary | null;
  onClose: () => void;
  onCopy: (text: string) => void;
}) {
  const bt = useBloemT();
  const organization = context?.scope === "organization";
  const create = useCreateInvitation(context);
  const { data: accessGroups = [] } = useAccessGroups(context);
  const { data: legacyLibraries = [] } = useAdminLibraries({ enabled: !organization });
  const { data: organizationLibraries = [] } = useOrganizationLibraries(
    context?.key ?? "organization:unavailable",
    organization,
  );
  const libraries = organization
    ? organizationLibraries.map((library) => ({
        id: library.folder_id,
        name: library.name,
        type: library.type,
        enabled: library.access_kind === "owned" || library.entitlement?.status === "active",
      }))
    : legacyLibraries;
  const [email, setEmail] = useState("");
  const [role, setRole] = useState("user");
  const [accessGroupID, setAccessGroupID] = useState<number | null>(null);
  const [libraryIDs, setLibraryIDs] = useState<number[] | null>(null);
  const [note, setNote] = useState("");
  const [createProfile, setCreateProfile] = useState(true);
  const [showTour, setShowTour] = useState(true);
  // After creation we keep the dialog open to show the claim link — the
  // token is only readable in this response, so this is the one chance to
  // copy it. emailSent changes the copy: delivered vs deliver-it-yourself.
  const [result, setResult] = useState<{ claimUrl: string; emailSent: boolean } | null>(null);

  const defaultGroup = useMemo(() => accessGroups.find((g) => g.is_default), [accessGroups]);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    const data = await create.send({
      email,
      role,
      access_group_id: effectiveAccessGroupID(role, accessGroupID),
      library_ids: libraryIDs,
      create_profile: createProfile,
      show_tour: showTour,
      note: note.trim() || undefined,
    });
    if (!data) return;
    if (data.email_sent)
      toast.success(
        bloemT("bloem.admin.orgInvitations.invitationSentTo", { email: data.invitation.email }),
      );
    if (data.claim_url) {
      setResult({ claimUrl: data.claim_url, emailSent: data.email_sent });
    } else {
      onClose();
    }
  }

  if (result) {
    return (
      <div className="min-w-0 space-y-4">
        <p className="text-sm">
          {result.emailSent
            ? bt("bloem.admin.orgInvitations.invitationEmailedYouCanAlso")
            : bt("bloem.admin.orgInvitations.theInvitationWasCreatedBut")}
        </p>
        <ClaimLinkBox
          claimUrl={result.claimUrl}
          finePrint={bt("bloem.admin.orgInvitations.newLinkFinePrint")}
          onCopy={onCopy}
          onDone={onClose}
        />
      </div>
    );
  }

  return (
    <form onSubmit={(event) => void handleSubmit(event)} className="space-y-4">
      <div className="space-y-2">
        <Label htmlFor="invitation-email">{bt("bloem.admin.orgInvitations.emailAddress")}</Label>
        <Input
          id="invitation-email"
          type="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder={bt("bloem.admin.orgInvitations.themExampleCom")}
          autoFocus
          required
        />
        <p className="text-muted-foreground text-xs">
          {bt("bloem.admin.orgInvitations.thisBecomesBothTheDestination")}
        </p>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-2">
          <Label>{bt("bloem.admin.orgInvitations.accessGroup")}</Label>
          <Select
            value={role === "admin" || accessGroupID === null ? "default" : String(accessGroupID)}
            onValueChange={(v) => setAccessGroupID(v === "default" ? null : Number(v))}
            disabled={role === "admin"}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="default">
                {defaultGroup
                  ? bt("bloem.admin.orgInvitations.default", { name: defaultGroup.name })
                  : bt("bloem.admin.orgInvitations.serverDefault")}
              </SelectItem>
              {accessGroups
                .filter((g) => !g.is_default)
                .map((g) => (
                  <SelectItem key={g.id} value={String(g.id)}>
                    {g.name}
                  </SelectItem>
                ))}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-2">
          <Label>{bt("bloem.admin.orgInvitations.role")}</Label>
          {organization ? (
            <p className="border-border bg-muted/30 rounded-md border px-3 py-2 text-sm">
              {bt("bloem.admin.orgInvitations.user")}
            </p>
          ) : (
            <Select value={role} onValueChange={setRole}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="user">{bt("bloem.admin.orgInvitations.user")}</SelectItem>
                <SelectItem value="admin">{bt("bloem.admin.orgInvitations.admin")}</SelectItem>
              </SelectContent>
            </Select>
          )}
        </div>
      </div>

      <LibraryAccessScope name={organization ? context?.name : undefined}>
        <LibraryAccessSelector
          libraries={libraries}
          value={libraryIDs}
          onChange={setLibraryIDs}
          allLabel={bt("bloem.admin.orgInvitations.inheritFromAccessGroup")}
          emptyHint={bt("bloem.admin.orgInvitations.followsGroupLibraryScope")}
        />
      </LibraryAccessScope>

      <div className="space-y-2">
        <Label htmlFor="invitation-note">
          {bt("bloem.admin.orgInvitations.personalNoteOptional")}
        </Label>
        <textarea
          id="invitation-note"
          value={note}
          onChange={(e) => setNote(e.target.value)}
          placeholder={bt("bloem.admin.orgInvitations.heySetYourselfUpWhenever")}
          rows={2}
          className="border-border bg-background text-foreground focus:border-ring focus:ring-ring/50 w-full resize-y rounded-md border px-3 py-2.5 text-sm shadow-xs transition-[color,box-shadow] outline-none focus:ring-[3px]"
        />
        <p className="text-muted-foreground text-xs">
          {bt("bloem.admin.orgInvitations.appearsInTheEmailPlain")}
        </p>
      </div>

      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <div>
            <Label htmlFor="invitation-create-profile">
              {bt("bloem.admin.orgInvitations.createTheirFirstProfile")}
            </Label>
            <p className="text-muted-foreground text-xs">
              {bt("bloem.admin.orgInvitations.namedFromThePartBefore")}
            </p>
          </div>
          <Switch
            id="invitation-create-profile"
            checked={createProfile}
            onCheckedChange={setCreateProfile}
          />
        </div>
        <div className="flex items-center justify-between">
          <div>
            <Label htmlFor="invitation-show-tour">
              {bt("bloem.admin.orgInvitations.showTheFeatureTourOn")}
            </Label>
            <p className="text-muted-foreground text-xs">
              {bt("bloem.admin.orgInvitations.walksThroughWhatThisServer")}
            </p>
          </div>
          <Switch id="invitation-show-tour" checked={showTour} onCheckedChange={setShowTour} />
        </div>
      </div>

      <div className="flex items-center justify-between pt-2">
        <p className="text-muted-foreground text-xs">
          {bt("bloem.admin.orgInvitations.linkExpiresIn7Days")}
        </p>
        <div className="flex gap-2">
          <Button type="button" variant="ghost" onClick={onClose}>
            {bt("bloem.admin.orgInvitations.cancel")}
          </Button>
          <Button type="submit" disabled={create.isPending}>
            {create.isPending
              ? organization
                ? bt("bloem.admin.orgInvitations.creating")
                : bt("bloem.admin.orgInvitations.sending")
              : organization
                ? bt("bloem.admin.orgInvitations.createInviteLink")
                : bt("bloem.admin.orgInvitations.sendInvite")}
          </Button>
        </div>
      </div>
    </form>
  );
}
