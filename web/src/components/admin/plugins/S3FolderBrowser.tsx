import { useCallback, useEffect, useRef, useState } from "react";
import { ChevronRight, Folder } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  type S3ConnectionInput,
  type S3FolderPage,
  useListS3Folders,
} from "@/hooks/queries/admin/storageSources";

import { browseErrorMessage } from "./s3Browse";

interface View {
  prefix: string;
  folders: S3FolderPage["folders"];
  nextCursor: string;
}

interface Failure {
  message: string;
  prefix: string;
  cursor?: string;
}

/** The prefixes along a slash-terminated path: "a/b/" gives a/ and a/b/. */
function trail(prefix: string): { name: string; prefix: string }[] {
  const names = prefix.split("/").filter(Boolean);
  return names.map((name, index) => ({ name, prefix: `${names.slice(0, index + 1).join("/")}/` }));
}

/**
 * An inline browser for the folders of one bucket, one level at a time. It only
 * chooses a prefix; the credentials it was opened with are held in memory for
 * the listing calls and never rendered.
 */
export function S3FolderBrowser({
  connection,
  bucket,
  onUse,
  onClose,
}: {
  connection: S3ConnectionInput;
  bucket: string;
  /** Called with the chosen prefix: slash-terminated, or empty for the bucket root. */
  onUse: (prefix: string) => void;
  onClose: () => void;
}) {
  const { mutateAsync, reset } = useListS3Folders();
  // Held as of opening: the form closes this browser whenever the connection changes.
  const [held] = useState(connection);
  const [view, setView] = useState<View | null>(null);
  const [failure, setFailure] = useState<Failure | null>(null);
  const [loading, setLoading] = useState(false);
  const latest = useRef(0);
  const started = useRef(false);

  const request = useCallback(
    async (prefix: string, cursor?: string) => {
      const id = ++latest.current;
      try {
        const page = await mutateAsync({ ...held, bucket, prefix, cursor });
        if (id !== latest.current) return;
        setFailure(null);
        setView((previous) => ({
          prefix,
          folders:
            cursor && previous?.prefix === prefix
              ? [...previous.folders, ...page.folders]
              : page.folders,
          nextCursor: page.nextCursor,
        }));
      } catch (error) {
        if (id !== latest.current) return;
        setFailure({ message: browseErrorMessage(error, "folders"), prefix, cursor });
      } finally {
        if (id === latest.current) setLoading(false);
      }
    },
    [mutateAsync, held, bucket],
  );

  // The observer keeps the last request, keys included, until it is reset.
  useEffect(() => reset, [reset]);

  useEffect(() => {
    if (started.current) return;
    started.current = true;
    void request("");
  }, [request]);

  function go(prefix: string, cursor?: string) {
    setLoading(true);
    void request(prefix, cursor);
  }

  const prefix = view?.prefix ?? "";
  const crumbs = trail(prefix);

  return (
    <section aria-label="Folder browser" className="space-y-2 rounded-md border p-3 text-sm">
      <nav aria-label="Folder path" className="flex flex-wrap items-center gap-0.5">
        {[{ name: bucket, prefix: "" }, ...crumbs].map((crumb, index, all) => {
          const current = index === all.length - 1;
          return (
            <span key={crumb.prefix} className="inline-flex items-center gap-0.5">
              {index > 0 ? (
                <ChevronRight className="text-muted-foreground h-3 w-3" aria-hidden />
              ) : null}
              {current ? (
                <span aria-current="page" className="px-1.5 font-medium">
                  {crumb.name}
                </span>
              ) : (
                <Button
                  type="button"
                  variant="ghost"
                  size="xs"
                  disabled={loading}
                  onClick={() => go(crumb.prefix)}
                >
                  {crumb.name}
                </Button>
              )}
            </span>
          );
        })}
      </nav>

      {failure ? (
        <div role="alert" className="text-destructive space-y-2">
          <p>{failure.message}</p>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={loading}
            onClick={() => go(failure.prefix, failure.cursor)}
          >
            Try again
          </Button>
        </div>
      ) : null}

      {view === null && !failure ? (
        <p className="text-muted-foreground">Loading folders...</p>
      ) : null}

      {view ? (
        view.folders.length === 0 ? (
          <p className="text-muted-foreground">No subfolders here.</p>
        ) : (
          <ul className="max-h-48 space-y-0.5 overflow-y-auto">
            {view.folders.map((folder) => (
              <li key={folder.prefix}>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="w-full justify-start"
                  disabled={loading}
                  onClick={() => go(folder.prefix)}
                >
                  <Folder className="h-3.5 w-3.5" aria-hidden />
                  {folder.name}
                </Button>
              </li>
            ))}
          </ul>
        )
      ) : null}

      {view && view.nextCursor !== "" ? (
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={loading}
          onClick={() => go(view.prefix, view.nextCursor)}
        >
          Load more
        </Button>
      ) : null}

      <div className="flex flex-wrap gap-2 pt-1">
        {prefix !== "" ? (
          <Button type="button" size="sm" onClick={() => onUse(prefix)}>
            Use this folder
          </Button>
        ) : null}
        <Button type="button" variant="outline" size="sm" onClick={() => onUse("")}>
          Use bucket root
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onClose}>
          Cancel
        </Button>
      </div>
    </section>
  );
}
