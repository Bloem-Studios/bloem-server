import { useRef, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
} from "@/api/client";
import { nativeApiWithProfileRequestContext } from "@/api/bloemClient";
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
      <h2 id="direct-profile-heading" className="text-xl font-medium">
        Separate sign-in for a profile
      </h2>
      <p className="text-muted-foreground text-sm">
        Give a household profile its own email and password, so it can sign in to the Bloem apps
        without the account password. The website still uses the account sign-in. To change this,
        you need your account password; accounts that only sign in through single sign-on can’t use
        it.
      </p>
      {capability.isPending ? (
        <p role="status">Checking support…</p>
      ) : capability.isError ? (
        <div role="alert">
          <p>{capability.error.message}</p>
          <Button variant="outline" onClick={() => void capability.refetch()}>
            Try again
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
          This server doesn’t support separate profile sign-ins.
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
          Try again
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
      setError("Enter your account password and tick the box to confirm.");
      return;
    }
    if (!disabling && (!email || !password)) {
      setError("Enter an email and a new password for the profile.");
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
            ? "The profile’s own sign-in is off, and it has been signed out of its apps."
            : "Sign-in saved. The profile was signed out of its apps and must sign in again.",
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
          Try again
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
          ? `This profile signs in as ${status.data.login_email}`
          : "This profile has no sign-in of its own."}
      </p>
      <fieldset disabled={pending || status.isFetching} className="space-y-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor="profile-login-email">Profile email</Label>
            <Input
              id="profile-login-email"
              name="login_email"
              type="email"
              autoComplete="off"
              defaultValue={status.data.login_email}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="profile-login-password">New password for the profile</Label>
            <Input
              id="profile-login-password"
              name="password"
              type="password"
              autoComplete="new-password"
            />
          </div>
        </div>
        <div className="space-y-2">
          <Label htmlFor="credential-account-password">Your account password</Label>
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
          I understand this signs the profile out of every app it signed in to with its own sign-in.
        </label>
        <div className="flex flex-wrap gap-3">
          <Button type="submit" value="save">
            {pending ? "Saving…" : "Save sign-in"}
          </Button>
          {status.data.configured && (
            <Button type="submit" value="disable" formNoValidate variant="destructive">
              Turn off its sign-in
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
