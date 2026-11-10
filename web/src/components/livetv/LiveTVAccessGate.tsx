import type { ReactNode } from "react";
import { Link } from "react-router";
import { Radio } from "lucide-react";
import { useBloemT } from "@/bloem/i18n";
import { Button } from "@/components/ui/button";
import { useLiveTVAccess } from "@/hooks/queries/useLiveTVAccess";

/** Mount protected queries and playback only after this viewer's grant resolves. */
export function LiveTVAccessGate({ children }: { children: ReactNode }) {
  const bt = useBloemT();
  const access = useLiveTVAccess();
  if (!access.isError && access.data?.supported && access.data.allowed) return children;

  const unsupported =
    access.data?.supported === false ||
    (access.isError && "status" in access.error && access.error.status === 404);
  const failed = access.isError && !unsupported;
  const denied = !access.isError && access.data?.allowed === false;
  const title = unsupported
    ? bt("bloem.liveTv.accessGate.unsupported")
    : failed
      ? bt("bloem.liveTv.accessGate.checkFailed")
      : denied
        ? bt("bloem.liveTv.accessGate.notEnabled")
        : bt("bloem.liveTv.accessGate.checking");
  return (
    <section
      className="mx-auto flex min-h-[50vh] max-w-xl flex-col items-center justify-center gap-5 px-6 text-center"
      aria-live="polite"
    >
      <Radio className="text-muted-foreground h-10 w-10" aria-hidden />
      <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
      {denied && (
        <p className="text-muted-foreground">{bt("bloem.liveTv.accessGate.askAdministrator")}</p>
      )}
      {failed && (
        <Button onClick={() => void access.refetch()}>{bt("bloem.liveTv.tryAgain")}</Button>
      )}
      {(failed || denied || unsupported) && (
        <Button variant="ghost" asChild>
          <Link to="/">{bt("bloem.liveTv.accessGate.backToHome")}</Link>
        </Button>
      )}
    </section>
  );
}
