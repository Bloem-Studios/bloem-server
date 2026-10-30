import { useCallback, useEffect, useRef, useState } from "react";
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
/** The bucket a folder of an all-buckets source is in: the first part of its path. */
function bucketOf(dir: StorageDirectory): string {
  return dir.logical_path.split("/")[0] ?? "";
}

export function StorageFolderBrowser({ open, onOpenChange, api, source, onSelect }: Props) {
  // An all-buckets source lists its buckets as the root's folders.
  const allBuckets = isAllBucketsSource(source);
  const rootName = (dir: StorageDirectory) => (allBuckets ? "All buckets" : dir.name || "Root");
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
          setError(e instanceof Error ? e.message : "Could not browse this source.");
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
        name: allBuckets && dir.logical_path === "" ? "All buckets" : dir.name,
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
          <DialogTitle>Browse {storageSourceLabel(source)}</DialogTitle>
          <DialogDescription>
            {allBuckets
              ? "Each bucket is a folder. Tick whole buckets or folders inside them, in as many buckets as you like."
              : "Choose folders for this library."}
          </DialogDescription>
          <p className="text-muted-foreground text-xs">
            Pick the folders that hold this library&apos;s titles. One folder per movie or show
            works best.
          </p>
        </DialogHeader>
        <div className="space-y-3">
          <nav aria-label="Storage folder breadcrumbs" className="flex flex-wrap gap-1">
            {page?.breadcrumbs.map((dir, i) => (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                key={i}
                disabled={loading}
                onClick={() => void load(dir.selection_ref)}
              >
                {i === 0 ? rootName(dir) : dir.name || "Root"}
              </Button>
            ))}
            {page && (
              <span aria-current="page" className="px-3 py-2 text-sm font-medium">
                {atRoot ? rootName(page.current) : page.current.name || "Root"}
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
              Up
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={loading}
              onClick={() => void load(page?.current.selection_ref)}
            >
              Refresh
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
                Retry
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
                  aria-label={`Select ${dir.name}`}
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
                Loading folders…
              </p>
            )}
            {page && !loading && !page.directories.length && !page.next_cursor && (
              <p className="text-muted-foreground p-3 text-sm">
                {allBuckets && atRoot
                  ? "These keys can't see any buckets yet."
                  : source.connection_summary?.prefix && !page.breadcrumbs.length
                    ? `This source starts at ${source.connection_summary.prefix} and has no subfolders. Use the current folder, or edit the source's prefix.`
                    : "No subfolders. You can use the current folder."}
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
              Load more folders
            </Button>
          )}
          {selected.length > 0 && (
            <div className="space-y-1">
              <p role="status" className="text-muted-foreground text-sm">
                {allBuckets
                  ? `${selected.length} folder${selected.length === 1 ? "" : "s"} in ${
                      new Set(selected.map(bucketOf)).size
                    } bucket${new Set(selected.map(bucketOf)).size === 1 ? "" : "s"}`
                  : `${selected.length} folder${selected.length === 1 ? "" : "s"} selected`}
              </p>
              <ul className="space-y-0.5 text-xs">
                {selected.map((dir) => (
                  <li key={dir.selection_ref} className="flex items-center gap-2">
                    <span className="min-w-0 flex-1 truncate">{dir.logical_path || dir.name}</span>
                    <Button
                      type="button"
                      variant="ghost"
                      size="xs"
                      aria-label={`Remove ${dir.logical_path || dir.name}`}
                      onClick={() => toggle(dir)}
                    >
                      Remove
                    </Button>
                  </li>
                ))}
              </ul>
            </div>
          )}
          {allBuckets && atRoot && selected.length === 0 && (
            <p className="text-muted-foreground text-xs">
              Every bucket these keys can see, including buckets added later, becomes part of this
              library.
            </p>
          )}
        </div>
        <DialogFooter className="flex-wrap gap-2">
          <Button type="button" variant="outline" onClick={close}>
            Cancel
          </Button>
          {selected.length === 0 && (
            <Button
              type="button"
              variant="outline"
              disabled={loading || !page || !!error}
              onClick={() => page && commit([page.current])}
            >
              {allBuckets && atRoot ? "Use all buckets" : "Use Current Folder"}
            </Button>
          )}
          <Button
            type="button"
            disabled={loading || !selected.length || !!error}
            onClick={() => commit(selected)}
          >
            Add selected folders
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
