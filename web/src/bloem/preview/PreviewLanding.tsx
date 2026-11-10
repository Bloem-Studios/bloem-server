import { useEffect, useState } from "react";
import { useBloemT } from "@/bloem/i18n";
import { Button } from "@/components/ui/button";
import { useBranding } from "@/hooks/useBranding";
import { discoverPreviewHost, type PreviewStatus } from "./api";
import { safePreviewHref, subscriptionHref } from "./routes";
export default function PreviewLanding() {
  const bt = useBloemT();
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
      <h1 className="text-3xl font-bold">{bt("bloem.preview.landing.title", { brand })}</h1>
      <p className="text-muted-foreground max-w-md">{bt("bloem.preview.landing.body")}</p>
      <div className="flex flex-wrap justify-center gap-3">
        {status?.enabled && launch && (
          <Button variant="outline" asChild>
            <a href={launch}>{bt("bloem.preview.landing.browse")}</a>
          </Button>
        )}
        {cta && (
          <Button asChild>
            <a href={cta}>
              {status?.cta_label || bt("bloem.preview.cta.subscribeToBrand", { brand })}
            </a>
          </Button>
        )}
      </div>
    </main>
  );
}
