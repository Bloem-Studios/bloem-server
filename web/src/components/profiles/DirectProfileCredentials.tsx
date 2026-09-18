import { useRef, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  nativeApiWithProfileRequestContext,
  StaleApiRequestContextError,
} from "@/api/client";
import { useAuth } from "@/hooks/useAuth";
import { useProfiles } from "@/hooks/queries/profiles";
import { useBloemCapabilities } from "@/hooks/queries/useBloemCapabilities";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

interface CredentialStatus {
  profile_id: string;
  configured: boolean;
  login_email: string;
  credential_revision: number;
}

export function DirectProfileCredentials() {
  const { user, profile } = useAuth();
  const capability = useBloemCapabilities();
  const authority = captureProfileRequestContext();
  const allowed = user?.role === "admin" || profile?.is_primary;
  if (!allowed || !authority) return null;
  return (
    <section className="mt-8 space-y-4 border-t pt-6" aria-labelledby="direct-profile-heading">
      <h2 id="direct-profile-heading" className="text-xl font-semibold">
        Profile sign-in credentials
      </h2>
      <p className="text-muted-foreground text-sm">
        Create a separate sign-in for a household profile in supported native clients. The browser
        still uses account sign-in: direct-profile credentials do not authorize its Silo v2 routes.
        Changes require your account’s enabled local password; SSO-only reauthentication is not
        supported here.
      </p>
      {capability.isPending ? (
        <p role="status">Checking support…</p>
      ) : capability.isError ? (
        <div role="alert">
          <p>{capability.error.message}</p>
          <Button variant="outline" onClick={() => void capability.refetch()}>
            Retry feature check
          </Button>
        </div>
      ) : capability.data?.feature_tokens?.includes("profile_credential_management_v1") ? (
        <HouseholdCredentials
          key={JSON.stringify([
            authority.serverOrigin,
            authority.authContextVersion,
            authority.profileId,
            authority.profileTokenGeneration,
          ])}
        />
      ) : (
        <p className="text-muted-foreground text-sm">
          This server does not provide household credential management.
        </p>
      )}
    </section>
  );
}
function HouseholdCredentials() {
  const profiles = useProfiles();
  const [selected, setSelected] = useState("");
  const target = profiles.data.find((profile) => profile.id === selected);
  if (profiles.isLoading) return <p role="status">Loading household profiles…</p>;
  if (profiles.isError)
    return (
      <div role="alert">
        <p>Could not load household profiles.</p>
        <Button variant="outline" onClick={() => void profiles.refetch()}>
          Reload household profiles
        </Button>
      </div>
    );
  return (
    <div className="space-y-4">
      <Label htmlFor="credential-profile">Profile</Label>
      <select
        id="credential-profile"
        className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
        value={selected}
        onChange={(event) => setSelected(event.target.value)}
      >
        <option value="">Choose a profile</option>
        {profiles.data.map((profile) => (
          <option key={profile.id} value={profile.id}>
            {profile.name}
          </option>
        ))}
      </select>
      {target && <CredentialForm key={target.id} id={target.id} name={target.name} />}
    </div>
  );
}
function CredentialForm({ id, name }: { id: string; name: string }) {
  const authority = captureProfileRequestContext();
  const client = useQueryClient();
  const key = [
    "profile-credential-status",
    authority?.serverOrigin,
    authority?.authContextVersion,
    authority?.profileId,
    authority?.profileTokenGeneration,
    id,
  ];
  const [pending, setPending] = useState(false);
  const busy = useRef(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const status = useQuery({
    queryKey: key,
    enabled: Boolean(authority),
    retry: false,
    queryFn: async ({ signal }) => {
      if (!authority || !isCapturedProfileAuthorityActive(authority))
        throw new StaleApiRequestContextError();
      const data = await nativeApiWithProfileRequestContext<CredentialStatus>(
        `/profile-credentials/${encodeURIComponent(id)}`,
        authority,
        { signal },
      );
      if (!isCapturedProfileAuthorityActive(authority)) throw new StaleApiRequestContextError();
      return data;
    },
  });
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (
      busy.current ||
      !authority ||
      !isCapturedProfileAuthorityActive(authority) ||
      !status.isSuccess ||
      status.isFetching
    )
      return;
    const form = event.currentTarget;
    const body = new FormData(form);
    const disabling =
      (event.nativeEvent as SubmitEvent).submitter?.getAttribute("value") === "disable";
    const current = String(body.get("current_password") ?? "");
    const password = String(body.get("password") ?? "");
    const email = String(body.get("login_email") ?? "").trim();
    // The checkbox belongs to the displayed revision. A changed revision
    // remounts the form, discarding both its confirmation and password drafts.
    const expectedRevision = Number(body.get("confirm"));
    if (!current || !body.has("confirm") || expectedRevision !== status.data.credential_revision) {
      setError("Enter your account password and confirm the session impact.");
      return;
    }
    if (!disabling && (!email || !password)) {
      setError("Enter a profile login email and new password.");
      return;
    }
    busy.current = true;
    setPending(true);
    setError("");
    setNotice("");
    try {
      await nativeApiWithProfileRequestContext(
        `/profile-credentials/${encodeURIComponent(id)}`,
        authority,
        {
          method: disabling ? "DELETE" : "PUT",
          body: JSON.stringify({
            current_password: current,
            expected_revision: expectedRevision,
            ...(disabling ? {} : { login_email: email, password }),
          }),
        },
        "none",
      );
      if (isCapturedProfileAuthorityActive(authority))
        setNotice(
          disabling
            ? "Direct profile sign-in disabled; its direct sessions have been revoked."
            : "Profile credentials saved; previous direct sessions have been revoked.",
        );
    } catch (cause) {
      if (isCapturedProfileAuthorityActive(authority))
        setError(cause instanceof Error ? cause.message : "Could not confirm the change.");
    } finally {
      // Secrets live only in this form/request, never a query or mutation cache.
      form.reset();
      try {
        // Keep actions blocked through authoritative readback, including after
        // conflicts or uncertain failures. A failed readback shows only Reload.
        if (isCapturedProfileAuthorityActive(authority))
          await client.invalidateQueries({ queryKey: key, exact: true });
      } finally {
        busy.current = false;
        setPending(false);
      }
    }
  }
  if (status.isPending) return <p role="status">Checking profile credential status…</p>;
  if (status.isError)
    return (
      <div role="alert">
        <p>{status.error.message}</p>
        <Button variant="outline" onClick={() => void status.refetch()}>
          Reload credential status
        </Button>
      </div>
    );
  return (
    <form
      key={status.data.credential_revision}
      onSubmit={(event) => void submit(event)}
      className="space-y-4"
      aria-label={`Manage sign-in for ${name}`}
    >
      <p className="text-sm">
        {status.data.configured
          ? `Direct sign-in enabled for ${status.data.login_email}`
          : "Direct sign-in is disabled for this profile."}
      </p>
      <fieldset disabled={pending || status.isFetching} className="space-y-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor="profile-login-email">Profile login email</Label>
            <Input
              id="profile-login-email"
              name="login_email"
              type="email"
              autoComplete="off"
              defaultValue={status.data.login_email}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="profile-login-password">New profile password</Label>
            <Input
              id="profile-login-password"
              name="password"
              type="password"
              autoComplete="new-password"
            />
          </div>
        </div>
        <div className="space-y-2">
          <Label htmlFor="credential-account-password">Your current account password</Label>
          <Input
            id="credential-account-password"
            name="current_password"
            type="password"
            autoComplete="current-password"
            required
          />
        </div>
        <label className="flex items-start gap-2 text-sm">
          <input
            type="checkbox"
            name="confirm"
            value={status.data.credential_revision}
            required
            className="accent-primary mt-1 size-4"
          />
          I understand that changing or disabling these credentials ends this profile’s direct
          sign-in sessions.
        </label>
        <div className="flex flex-wrap gap-3">
          <Button type="submit" value="save">
            {pending ? "Saving…" : "Save profile credentials"}
          </Button>
          {status.data.configured && (
            <Button type="submit" value="disable" formNoValidate variant="destructive">
              Disable direct sign-in
            </Button>
          )}
        </div>
      </fieldset>
      {error && (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      )}
      {notice && (
        <p role="status" className="text-sm">
          {notice}
        </p>
      )}
    </form>
  );
}
