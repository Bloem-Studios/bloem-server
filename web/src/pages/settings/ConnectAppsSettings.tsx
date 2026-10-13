import { useAuth } from "@/hooks/useAuth";
import { SILO_APP_EXAMPLES } from "./connectApps";

export default function ConnectAppsSettings() {
  const { user } = useAuth();
  const serverURL = typeof window === "undefined" ? "" : window.location.origin;
  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-lg font-semibold">Connect an app</h2>
        <p className="text-muted-foreground mt-1 text-sm">{SILO_APP_EXAMPLES}</p>
      </div>
      <dl className="surface-panel-subtle space-y-4 rounded-xl border p-5">
        <div>
          <dt className="text-muted-foreground text-sm">Server address</dt>
          <dd className="mt-1 font-mono break-all">{serverURL}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground text-sm">Account username</dt>
          <dd className="mt-1">{user?.username ?? ""}</dd>
        </div>
      </dl>
      <p className="text-muted-foreground text-sm">
        Use the same sign-in method as this website. Choose your household profile after signing in.
        If the profile has a PIN, enter it when prompted.
      </p>
    </div>
  );
}
