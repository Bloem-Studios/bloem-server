import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AppWindow, KeyRound, Plus, ShieldOff } from "lucide-react";
import { adminV2Api, adminV2QueryKey, AdminV2ClientError } from "@/api/adminV2Client";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

export interface CompatibilityApplication {
  instance_id: string;
  kind: string;
  state: string;
  enabled: boolean;
  revoked: boolean;
  healthy: boolean;
  version: string;
  image_digest: string;
  api_range: { min: string; max: string };
  last_contact_at: string | null;
  active_sessions: number;
  capabilities: string[];
  revision: number;
  canonical_url: string;
  commands: { install: string; update: string; rollback: string; remove: string };
}

interface CompatibilityEnrollment {
  kind: string;
  secret: string;
  expires_at: string;
}

const compatibilityKeys = {
  applications: [...adminV2QueryKey("platform"), "compatibility", "applications"] as const,
};

function useCompatibilityApplications() {
  return useQuery({
    queryKey: compatibilityKeys.applications,
    queryFn: () =>
      adminV2Api<{ applications: CompatibilityApplication[] }>(
        "/platform/compatibility/applications",
      ),
  });
}

function stateBadgeVariant(state: string): "default" | "secondary" | "destructive" {
  if (state === "enabled") return "default";
  if (state === "revoked" || state === "unhealthy" || state === "incompatible") {
    return "destructive";
  }
  return "secondary";
}

function CommandRow({ label, command }: { label: string; command: string }) {
  return (
    <div className="space-y-1">
      <p className="text-muted-foreground text-xs font-medium uppercase">{label}</p>
      <pre className="bg-muted overflow-x-auto rounded-md p-2 text-xs whitespace-pre-wrap">
        {command}
      </pre>
    </div>
  );
}

function DetailRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-baseline justify-between gap-2 text-sm">
      <span className="text-muted-foreground">{label}</span>
      <span className="font-mono text-xs break-all">{children}</span>
    </div>
  );
}

function EnrollmentDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange(open: boolean): void;
}) {
  const queryClient = useQueryClient();
  const [kind, setKind] = useState("jellyfin");
  const [capabilities, setCapabilities] = useState("");
  const [enrollment, setEnrollment] = useState<CompatibilityEnrollment | null>(null);
  const mutation = useMutation({
    mutationFn: (input: { kind: string; capabilities: string[] }) =>
      adminV2Api<{ enrollment: CompatibilityEnrollment }>("/platform/compatibility/enrollments", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: async (result) => {
      setEnrollment(result.enrollment);
      await queryClient.invalidateQueries({ queryKey: compatibilityKeys.applications });
    },
  });

  function close(openState: boolean) {
    onOpenChange(openState);
    if (!openState) {
      setKind("jellyfin");
      setCapabilities("");
      setEnrollment(null);
      mutation.reset();
    }
  }

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New enrollment</DialogTitle>
          <DialogDescription>
            Creates a short-lived, single-use enrollment secret. Mount it as a Docker secret on the
            companion; never place it in an environment variable.
          </DialogDescription>
        </DialogHeader>
        {enrollment ? (
          <div className="space-y-3">
            <p className="text-sm">
              Enrollment secret for <strong>{enrollment.kind}</strong> — it is shown only once and
              expires at {new Date(enrollment.expires_at).toLocaleString()}:
            </p>
            <pre className="bg-muted overflow-x-auto rounded-md p-3 font-mono text-sm break-all">
              {enrollment.secret}
            </pre>
          </div>
        ) : (
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="enrollment-kind">Application</Label>
              <select
                id="enrollment-kind"
                className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
                value={kind}
                onChange={(event) => setKind(event.target.value)}
              >
                <option value="jellyfin">Jellyfin</option>
                <option value="audiobookshelf">Audiobookshelf</option>
              </select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="enrollment-capabilities">Capabilities (comma-separated)</Label>
              <Input
                id="enrollment-capabilities"
                placeholder="identity.exchange, catalog.read"
                value={capabilities}
                onChange={(event) => setCapabilities(event.target.value)}
              />
            </div>
            {mutation.error ? (
              <p className="text-destructive text-sm" role="alert">
                {mutation.error.message}
              </p>
            ) : null}
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => close(false)}>
            {enrollment ? "Done" : "Cancel"}
          </Button>
          {enrollment ? null : (
            <Button
              disabled={mutation.isPending}
              onClick={() =>
                mutation.mutate({
                  kind,
                  capabilities: capabilities
                    .split(",")
                    .map((value) => value.trim())
                    .filter(Boolean),
                })
              }
            >
              {mutation.isPending ? "Creating…" : "Create enrollment"}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ApplicationCard({ application }: { application: CompatibilityApplication }) {
  const queryClient = useQueryClient();
  const [actionError, setActionError] = useState("");
  const [revokeOpen, setRevokeOpen] = useState(false);
  const [credential, setCredential] = useState("");

  const act = useMutation({
    mutationFn: (input: { action: string; body: Record<string, unknown> }) =>
      adminV2Api<{ credential?: { secret: string } }>(
        `/platform/compatibility/applications/${encodeURIComponent(application.instance_id)}/${input.action}`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(input.body),
        },
      ),
    onSuccess: async (result) => {
      setActionError("");
      if (result?.credential?.secret) setCredential(result.credential.secret);
      await queryClient.invalidateQueries({ queryKey: compatibilityKeys.applications });
    },
    onError: (error) => {
      setActionError(error instanceof AdminV2ClientError ? error.message : String(error));
    },
  });

  const revision = { expected_revision: application.revision };

  return (
    <article className="surface-panel space-y-4 rounded-2xl p-5">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <AppWindow className="text-muted-foreground h-5 w-5" />
          <div>
            <h2 className="text-lg font-semibold capitalize">{application.kind}</h2>
            <p className="text-muted-foreground font-mono text-xs">{application.instance_id}</p>
          </div>
        </div>
        <Badge variant={stateBadgeVariant(application.state)}>{application.state}</Badge>
      </header>

      <div className="grid gap-2 sm:grid-cols-2">
        <div className="space-y-2">
          <DetailRow label="Version">{application.version || "—"}</DetailRow>
          <DetailRow label="Image digest">{application.image_digest || "—"}</DetailRow>
          <DetailRow label="API range">
            {application.api_range.min} – {application.api_range.max}
          </DetailRow>
          <DetailRow label="Last contact">
            {application.last_contact_at
              ? new Date(application.last_contact_at).toLocaleString()
              : "never"}
          </DetailRow>
        </div>
        <div className="space-y-2">
          <DetailRow label="Active sessions">{application.active_sessions}</DetailRow>
          <DetailRow label="Capabilities">
            {application.capabilities.length > 0 ? application.capabilities.join(", ") : "—"}
          </DetailRow>
          <DetailRow label="Client URL">{application.canonical_url}</DetailRow>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        {application.enabled ? (
          <Button
            variant="outline"
            disabled={act.isPending}
            onClick={() => act.mutate({ action: "disable", body: revision })}
          >
            Disable
          </Button>
        ) : (
          <Button
            disabled={act.isPending || application.revoked}
            onClick={() => act.mutate({ action: "enable", body: revision })}
          >
            Enable
          </Button>
        )}
        <Button
          variant="outline"
          disabled={act.isPending}
          onClick={() => act.mutate({ action: "rotate-credential", body: revision })}
        >
          <KeyRound className="mr-1 h-4 w-4" />
          Rotate credential
        </Button>
        <Button variant="destructive" disabled={act.isPending} onClick={() => setRevokeOpen(true)}>
          <ShieldOff className="mr-1 h-4 w-4" />
          Revoke
        </Button>
      </div>
      {actionError ? (
        <p className="text-destructive text-sm" role="alert">
          {actionError}
        </p>
      ) : null}
      {credential ? (
        <div className="space-y-1">
          <p className="text-sm">New service credential — it is shown only once:</p>
          <pre className="bg-muted overflow-x-auto rounded-md p-2 font-mono text-xs break-all">
            {credential}
          </pre>
        </div>
      ) : null}

      <section className="space-y-3">
        <p className="text-muted-foreground text-sm">
          Run these on the host (or through your deployment controller). Bloem displays them and
          never runs Docker itself.
        </p>
        <CommandRow label="Install" command={application.commands.install} />
        <CommandRow label="Update" command={application.commands.update} />
        <CommandRow label="Rollback" command={application.commands.rollback} />
        <CommandRow label="Remove" command={application.commands.remove} />
      </section>

      <Dialog open={revokeOpen} onOpenChange={setRevokeOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Revoke {application.kind}?</DialogTitle>
            <DialogDescription>
              Revocation invalidates the application's service credentials immediately. Its routes
              answer unavailable until it is enrolled again.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setRevokeOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={act.isPending}
              onClick={() => {
                setRevokeOpen(false);
                act.mutate({ action: "revoke", body: { ...revision, confirmed: true } });
              }}
            >
              Revoke credentials
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </article>
  );
}

export default function CompatibilityApplicationsPage() {
  useDocumentTitle("Compatibility Applications");
  const [enrollOpen, setEnrollOpen] = useState(false);
  const applications = useCompatibilityApplications();

  return (
    <section className="admin-page space-y-6">
      <div className="page-header">
        <div className="space-y-2">
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
            Compatibility Applications
          </h1>
          <p className="page-subtitle text-sm sm:text-base">
            Enroll, enable, and monitor the removable Jellyfin and Audiobookshelf applications.
            Containers are operated on the host; Bloem shows the exact commands and never runs them.
          </p>
        </div>
        <Button onClick={() => setEnrollOpen(true)}>
          <Plus className="mr-2 h-4 w-4" />
          New enrollment
        </Button>
      </div>

      {applications.isLoading ? (
        <div className="space-y-3" role="status" aria-label="Loading compatibility applications">
          {Array.from({ length: 2 }, (_, index) => (
            <Skeleton key={index} className="h-40 w-full" />
          ))}
        </div>
      ) : applications.isError ? (
        <div
          className="border-destructive/30 bg-destructive/10 text-destructive rounded-xl border p-4"
          role="alert"
        >
          {applications.error.message}
        </div>
      ) : (applications.data?.applications.length ?? 0) === 0 ? (
        <div className="surface-panel text-muted-foreground rounded-2xl p-10 text-center">
          <AppWindow className="mx-auto mb-3 h-8 w-8" />
          No compatibility applications are enrolled. Create an enrollment, mount the secret on the
          companion, and start it with the install command.
        </div>
      ) : (
        <div className="space-y-4">
          {applications.data?.applications.map((application) => (
            <ApplicationCard key={application.instance_id} application={application} />
          ))}
        </div>
      )}

      <EnrollmentDialog open={enrollOpen} onOpenChange={setEnrollOpen} />
    </section>
  );
}
