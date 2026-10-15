import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { useBranding } from "@/hooks/useBranding";
import { discoverPreviewHost, type PreviewStatus } from "./api";
import { safePreviewHref, subscriptionHref } from "./routes";
export default function PreviewLanding() {
  const [status, setStatus] = useState<PreviewStatus | null>(null);
  const { serverName } = useBranding();
  useEffect(() => {
    let cancelled = false;
    void discoverPreviewHost().then((value) => {
      if (!cancelled) setStatus(value);
    });
    return () => {
      cancelled = true;
    };
  }, []);
  const brand = status?.brand || serverName;
  const launch = safePreviewHref(status?.launch_url);
  const cta = subscriptionHref(status?.cta_url || status?.storefront_url || "");
  return (
    <main className="page-shell flex min-h-[70vh] flex-col items-center justify-center gap-6 text-center">
      <h1 className="text-3xl font-bold">Start browsing from {brand}</h1>
      <p className="text-muted-foreground max-w-md">
        Use a fresh preview link to explore the library, or start your trial.
      </p>
      <div className="flex flex-wrap justify-center gap-3">
        {status?.enabled && launch && (
          <Button variant="outline" asChild>
            <a href={launch}>Browse the library</a>
          </Button>
        )}
        {cta && (
          <Button asChild>
            <a href={cta}>{status?.cta_label || `Subscribe to ${brand}`}</a>
          </Button>
        )}
      </div>
    </main>
  );
}
