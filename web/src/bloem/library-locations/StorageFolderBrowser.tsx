import { useCallback, useEffect, useRef, useState } from "react";
import { bloemT, useBloemT } from "@/bloem/i18n";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { isAllBucketsSource, storageSourceLabel } from "@/hooks/queries/admin/storageSources";
import type { LocationApi } from "./api";
import type { RootSelection, StorageDirectory, StoragePage, StorageSource } from "./types";
interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  api: LocationApi;
  source: StorageSource;
  onSelect: (roots: RootSelection[]) => void;
}
/** A folder's name in the browser; the root of a source without one is "Root". */
function folderName(dir: StorageDirectory): string {
  return dir.name || bloemT("bloem.admin.libraries.locations.browser.root");
}
/** The bucket a folder of an all-buckets source is in: the first part of its path. */
function bucketOf(dir: StorageDirectory): string {
  return dir.logical_path.split("/")[0] ?? "";
}

export function StorageFolderBrowser({ open, onOpenChange, api, source, onSelect }: Props) {
  const bt = useBloemT();
  // An all-buckets source lists its buckets as the root's folders.
  const allBuckets = isAllBucketsSource(source);
  const rootName = (dir: StorageDirectory) =>
    allBuckets ? bt("bloem.admin.libraries.locations.browser.allBuckets") : folderName(dir);
  const [page, setPage] = useState<StoragePage | null>(null);
  const [selected, setSelected] = useState<StorageDirectory[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const sequence = useRef(0);
  const abort = useRef<AbortController | null>(null);
  const retry = useRef({ ref: "", cursor: "", append: false });
  const atRoot = page !== null && page.breadcrumbs.length === 0;
  const load = useCallback(
    async (ref = "", cursor = "", append = false) => {
      abort.current?.abort();
      const controller = new AbortController();
      abort.current = controller;
      const request = ++sequence.current;
      retry.current = { ref, cursor, append };
      setLoading(true);
      setError("");
      if (!append) setPage(null);
      try {
        const next = await api.browse(source.source_key, ref, cursor, controller.signal);
        if (request !== sequence.current || controller.signal.aborted) return;
        setPage((old) => ({
          ...next,
          directories: append
            ? [...(old?.directories ?? []), ...next.directories]
            : next.directories,
        }));
      } catch (e) {
        if (request === sequence.current && !controller.signal.aborted)
          setError(
            e instanceof Error
              ? e.message
              : bloemT("bloem.admin.libraries.locations.browser.couldNotBrowse"),
          );
      } finally {
        if (request === sequence.current && !controller.signal.aborted) setLoading(false);
      }
    },
    [api, source.source_key],
  );
  const cancelRequests = useCallback(() => {
    sequence.current++;
    abort.current?.abort();
  }, []);
  useEffect(() => {
    setPage(null);
    setSelected([]);
    setError("");
    if (open) void load();
    return cancelRequests;
  }, [open, load, cancelRequests, source.configuration_revision]);
  function close() {
    sequence.current++;
    abort.current?.abort();
    setSelected([]);
    onOpenChange(false);
  }
  function commit(directories: StorageDirectory[]) {
    onSelect(
      directories.map((dir) => ({
        ...dir,
        // The root of an all-buckets source is every bucket, not the source's id.
        name:
          allBuckets && dir.logical_path === ""
            ? bt("bloem.admin.libraries.locations.browser.allBuckets")
            : dir.name,
        source_key: source.source_key,
        configuration_revision: source.configuration_revision,
      })),
    );
    close();
  }
  function toggle(dir: StorageDirectory) {
    setSelected((old) =>
      old.some((d) => d.selection_ref === dir.selection_ref)
        ? old.filter((d) => d.selection_ref !== dir.selection_ref)
        : [...old, dir],
    );
  }
  return (
    <Dialog
      open={open}
      onOpenChange={(value) => {
        if (!value) close();
      }}
    >
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>
            {bt("bloem.admin.libraries.locations.browser.title", {
              source: storageSourceLabel(source),
            })}
          </DialogTitle>
          <DialogDescription>
            {allBuckets
              ? bt("bloem.admin.libraries.locations.browser.bucketsDescription")
              : bt("bloem.admin.libraries.locations.browser.description")}
          </DialogDescription>
          <p className="text-muted-foreground text-xs">
            {bt("bloem.admin.libraries.locations.browser.hint")}
          </p>
        </DialogHeader>
        <div className="space-y-3">
          <nav
            aria-label={bt("bloem.admin.libraries.locations.browser.breadcrumbs")}
            className="flex flex-wrap gap-1"
          >
            {page?.breadcrumbs.map((dir, i) => (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                key={i}
                disabled={loading}
                onClick={() => void load(dir.selection_ref)}
              >
                {i === 0 ? rootName(dir) : folderName(dir)}
              </Button>
            ))}
            {page && (
              <span aria-current="page" className="px-3 py-2 text-sm font-medium">
                {atRoot ? rootName(page.current) : folderName(page.current)}
              </span>
            )}
          </nav>
          <div className="flex gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={loading || !page || !page.breadcrumbs.length}
              onClick={() =>
                void load(page!.breadcrumbs[page!.breadcrumbs.length - 1]!.selection_ref)
              }
            >
              {bt("bloem.admin.libraries.locations.browser.up")}
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={loading}
              onClick={() => void load(page?.current.selection_ref)}
            >
              {bt("bloem.admin.libraries.locations.browser.refresh")}
            </Button>
          </div>
          {error && (
            <div role="alert" className="text-destructive space-y-2 text-sm">
              <p>{error}</p>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() =>
                  void load(retry.current.ref, retry.current.cursor, retry.current.append)
                }
              >
                {bt("bloem.admin.libraries.locations.browser.retry")}
              </Button>
            </div>
          )}
          <div
            className="border-border max-h-72 overflow-y-auto rounded-md border"
            aria-busy={loading}
          >
            {page?.directories.map((dir, i) => (
              <div
                key={i}
                className="border-border flex items-center gap-2 border-b p-2 last:border-b-0"
              >
                <input
                  type="checkbox"
                  aria-label={bt("bloem.admin.libraries.locations.browser.select", {
                    name: dir.name,
                  })}
                  checked={selected.some((d) => d.selection_ref === dir.selection_ref)}
                  disabled={loading}
                  onChange={() => toggle(dir)}
                />
                <Button
                  type="button"
                  variant="ghost"
                  className="min-w-0 flex-1 justify-start truncate"
                  disabled={loading}
                  onClick={() => void load(dir.selection_ref)}
                >
                  {dir.name}
                </Button>
              </div>
            ))}
            {loading && (
              <p role="status" className="text-muted-foreground p-3 text-sm">
                {bt("bloem.admin.libraries.locations.browser.loading")}
              </p>
            )}
            {page && !loading && !page.directories.length && !page.next_cursor && (
              <p className="text-muted-foreground p-3 text-sm">
                {allBuckets && atRoot
                  ? bt("bloem.admin.libraries.locations.browser.noBuckets")
                  : source.connection_summary?.prefix && !page.breadcrumbs.length
                    ? bt("bloem.admin.libraries.locations.browser.emptyPrefix", {
                        prefix: source.connection_summary.prefix,
                      })
                    : bt("bloem.admin.libraries.locations.browser.noSubfolders")}
              </p>
            )}
          </div>
          {page?.next_cursor && (
            <Button
              type="button"
              variant="outline"
              disabled={loading}
              onClick={() => void load(page.current.selection_ref, page.next_cursor, true)}
            >
              {bt("bloem.admin.libraries.locations.browser.loadMore")}
            </Button>
          )}
          {selected.length > 0 && (
            <div className="space-y-1">
              <p role="status" className="text-muted-foreground text-sm">
                {allBuckets
                  ? bt("bloem.admin.libraries.locations.browser.selectedInBuckets", {
                      folders: selected.length,
                      buckets: new Set(selected.map(bucketOf)).size,
                    })
                  : bt("bloem.admin.libraries.locations.browser.selectedCount", {
                      count: selected.length,
                    })}
              </p>
              <ul className="space-y-0.5 text-xs">
                {selected.map((dir) => (
                  <li key={dir.selection_ref} className="flex items-center gap-2">
                    <span className="min-w-0 flex-1 truncate">{dir.logical_path || dir.name}</span>
                    <Button
                      type="button"
                      variant="ghost"
                      size="xs"
                      aria-label={bt("bloem.admin.libraries.locations.removeNamed", {
                        name: dir.logical_path || dir.name,
                      })}
                      onClick={() => toggle(dir)}
                    >
                      {bt("bloem.admin.libraries.locations.remove")}
                    </Button>
                  </li>
                ))}
              </ul>
            </div>
          )}
          {allBuckets && atRoot && selected.length === 0 && (
            <p className="text-muted-foreground text-xs">
              {bt("bloem.admin.libraries.locations.browser.allBucketsNote")}
            </p>
          )}
        </div>
        <DialogFooter className="flex-wrap gap-2">
          <Button type="button" variant="outline" onClick={close}>
            {bt("bloem.admin.libraries.locations.browser.cancel")}
          </Button>
          {selected.length === 0 && (
            <Button
              type="button"
              variant="outline"
              disabled={loading || !page || !!error}
              onClick={() => page && commit([page.current])}
            >
              {allBuckets && atRoot
                ? bt("bloem.admin.libraries.locations.browser.useAllBuckets")
                : bt("bloem.admin.libraries.locations.browser.useCurrentFolder")}
            </Button>
          )}
          <Button
            type="button"
            disabled={loading || !selected.length || !!error}
            onClick={() => commit(selected)}
          >
            {bt("bloem.admin.libraries.locations.browser.addSelected")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
