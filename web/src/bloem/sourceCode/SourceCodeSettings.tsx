import { useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Download, ExternalLink, LoaderCircle } from "lucide-react";

import { useBloemT } from "@/bloem/i18n";
import { PageHeader } from "@/components/page/PageHeader";
import { SettingsGroup } from "@/components/settings/SettingsGroup";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { formatFileSize } from "@/lib/mediaFormat";
import {
  AGPL_LICENSE_URL,
  SOURCE_CODE_FEATURE,
  isSourceUnavailable,
  prepareSourceDownload,
  shortRevision,
  startBrowserDownload,
  useBloemFeatureTokens,
  useSourceCodeInfo,
  type SourceCodeInfo,
} from "./api";

/**
 * Settings → Source code: the offer AGPL-3.0 section 13 asks of a network
 * server. Every profile can open it and download the complete source of the
 * version the server runs, free of charge.
 */
export default function SourceCodeSettings() {
  const bt = useBloemT();
  const capabilities = useBloemFeatureTokens();
  const offered = capabilities.data?.feature_tokens.includes(SOURCE_CODE_FEATURE) === true;
  const info = useSourceCodeInfo(offered);

  return (
    <div className="space-y-6">
      <PageHeader title={bt("bloem.sourceCode.title")} subtitle={bt("bloem.sourceCode.subtitle")} />

      <SettingsGroup
        title={bt("bloem.sourceCode.license.title")}
        description={bt("bloem.sourceCode.license.description")}
      >
        <a
          href={AGPL_LICENSE_URL}
          target="_blank"
          rel="noreferrer"
          className="text-foreground focus-visible:ring-ring rounded-sm text-sm font-medium break-words underline underline-offset-4 focus-visible:ring-2 focus-visible:outline-none"
        >
          {bt("bloem.sourceCode.license.link")}
          <ExternalLink className="ms-1.5 inline size-4 align-[-0.1875em]" aria-hidden="true" />
          <span className="sr-only">{bt("bloem.sourceCode.license.opensInNewTab")}</span>
        </a>
      </SettingsGroup>

      <SettingsGroup
        title={bt("bloem.sourceCode.download.title")}
        description={offered ? bt("bloem.sourceCode.download.description") : undefined}
      >
        {capabilities.isPending || (offered && info.isPending) ? (
          <div role="status" className="space-y-3">
            <span className="sr-only">{bt("bloem.sourceCode.state.loading")}</span>
            <Skeleton className="h-14 w-full max-w-sm rounded-lg" />
            <Skeleton className="h-11 w-full rounded-md sm:h-9 sm:w-52" />
          </div>
        ) : capabilities.isError ? (
          <Problem retry={() => void capabilities.refetch()}>
            {bt("bloem.sourceCode.state.checkFailed")}
          </Problem>
        ) : !offered ? (
          <p role="status" className="text-muted-foreground text-sm">
            {bt("bloem.sourceCode.state.notOffered")}
          </p>
        ) : info.isError ? (
          <Problem retry={() => void info.refetch()}>
            {isSourceUnavailable(info.error)
              ? bt("bloem.sourceCode.state.unavailable")
              : bt("bloem.sourceCode.state.loadFailed")}
          </Problem>
        ) : info.data ? (
          <SourceDownload info={info.data} />
        ) : null}
      </SettingsGroup>
    </div>
  );
}

function Problem({ children, retry }: { children: ReactNode; retry: () => void }) {
  const bt = useBloemT();
  return (
    <div className="space-y-3">
      <p role="status" className="text-muted-foreground text-sm">
        {children}
      </p>
      <Button variant="outline" className="h-11 text-base sm:h-9 sm:text-sm" onClick={retry}>
        {bt("bloem.sourceCode.state.retry")}
      </Button>
    </div>
  );
}

function SourceDownload({ info }: { info: SourceCodeInfo }) {
  const bt = useBloemT();
  const queryClient = useQueryClient();
  const [preparing, setPreparing] = useState(false);
  const [failure, setFailure] = useState<"unavailable" | "failed" | null>(null);
  const size = formatFileSize(info.size_bytes);

  // The address is built on the click, not at render: the login token it
  // carries has to be current when the browser opens it.
  async function download() {
    setPreparing(true);
    setFailure(null);
    try {
      const { url, fileName } = await prepareSourceDownload(queryClient);
      startBrowserDownload(url, fileName);
    } catch (error) {
      setFailure(isSourceUnavailable(error) ? "unavailable" : "failed");
    } finally {
      setPreparing(false);
    }
  }

  return (
    <div className="space-y-4">
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-6 gap-y-2 text-sm">
        <dt className="text-muted-foreground">{bt("bloem.sourceCode.download.version")}</dt>
        <dd className="min-w-0">
          <code className="font-mono break-all">{shortRevision(info.revision)}</code>
        </dd>
        {size ? (
          <>
            <dt className="text-muted-foreground">{bt("bloem.sourceCode.download.size")}</dt>
            <dd className="min-w-0 tabular-nums">{size}</dd>
          </>
        ) : null}
      </dl>
      <Button
        className="h-11 w-full text-base sm:h-9 sm:w-auto sm:text-sm"
        onClick={() => void download()}
        disabled={preparing}
      >
        {preparing ? (
          <LoaderCircle data-icon="inline-start" className="motion-safe:animate-spin" />
        ) : (
          <Download data-icon="inline-start" aria-hidden="true" />
        )}
        {preparing
          ? bt("bloem.sourceCode.download.preparing")
          : bt("bloem.sourceCode.download.button")}
      </Button>
      {failure ? (
        <p role="alert" className="text-destructive text-sm">
          {failure === "unavailable"
            ? bt("bloem.sourceCode.state.unavailable")
            : bt("bloem.sourceCode.download.failed")}
        </p>
      ) : null}
    </div>
  );
}
