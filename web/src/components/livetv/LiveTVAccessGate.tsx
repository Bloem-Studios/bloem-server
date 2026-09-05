import type { ReactNode } from "react";
import { Link } from "react-router";
import { Radio } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useLiveTVAccess } from "@/hooks/queries/useLiveTVAccess";

/** Mount protected queries and playback only after this viewer's grant resolves. */
export function LiveTVAccessGate({ children }: { children: ReactNode }) {
  const access = useLiveTVAccess();
  if (!access.isError && access.data?.supported && access.data.allowed) return children;

  const unsupported =
    access.data?.supported === false ||
    (access.isError && "status" in access.error && access.error.status === 404);
  const failed = access.isError && !unsupported;
  const denied = !access.isError && access.data?.allowed === false;
  const title = unsupported
    ? "Live TV isn’t available on this server"
    : failed
      ? "Couldn’t check Live TV access"
      : denied
        ? "Live TV isn’t enabled for this profile"
        : "Checking Live TV access…";
  return (
    <section
      className="mx-auto flex min-h-[50vh] max-w-xl flex-col items-center justify-center gap-5 px-6 text-center"
      aria-live="polite"
    >
      <Radio className="text-muted-foreground h-10 w-10" aria-hidden />
      <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
      {denied && (
        <p className="text-muted-foreground">Ask your server administrator for Live TV access.</p>
      )}
      {failed && <Button onClick={() => void access.refetch()}>Try again</Button>}
      {(failed || denied || unsupported) && (
        <Button variant="ghost" asChild>
          <Link to="/">Back to home</Link>
        </Button>
      )}
    </section>
  );
}
