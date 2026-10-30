import { useState } from "react";
import FolderBrowser from "@/components/FolderBrowser";
import PathAutocompleteInput from "@/components/PathAutocompleteInput";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { locationRootName, storageSourceLabel } from "@/hooks/queries/admin/storageSources";
import type { LibraryFormController } from "@/components/admin/libraries/useLibraryForm";
import { supportsSource } from "./types";
import { StorageFolderBrowser } from "./StorageFolderBrowser";
export function LibraryLocationsEditor({ form }: { form: LibraryFormController }) {
  const locations = form.locations;
  const [filesystemOpen, setFilesystemOpen] = useState(false);
  const [sourceKey, setSourceKey] = useState("");
  const [storageOpen, setStorageOpen] = useState(false);
  const paths = form.paths.filter((p) => p.trim());
  const retainedKeys = locations.original?.locations.map((l) => l.source_key) ?? [];
  const keys = new Set([...retainedKeys, ...locations.selections.map((s) => s.source_key)]);
  const mixing = locations.caps?.library_location_mixing === true;
  const canFilesystem = mixing || keys.size === 0;
  const usable = locations.sources.filter(
    (s) =>
      locations.caps &&
      supportsSource(locations.caps, s, form.type) &&
      (mixing ||
        (!paths.length &&
          !locations.original?.paths.length &&
          (keys.size === 0 || keys.has(s.source_key)))),
  );
  const source = usable.find((s) => s.source_key === sourceKey);
  const invalid = locations.shapeError(form.paths);
  return (
    <div className="space-y-3" aria-label="Library locations">
      {locations.loading && (
        <p role="status" className="text-muted-foreground text-sm">
          Loading locations…
        </p>
      )}
      {locations.error && (
        <p role="alert" className="text-destructive text-sm">
          {locations.error}
        </p>
      )}
      {invalid && (
        <p role="alert" className="text-destructive text-sm">
          {invalid}
        </p>
      )}
      {!locations.uncertainCreate &&
        (locations.conflict || (!locations.ready && !locations.loading)) && (
          <Button
            type="button"
            variant="outline"
            disabled={locations.pending}
            onClick={() => void locations.reload()}
          >
            Reload locations
          </Button>
        )}
      <fieldset disabled={!locations.ready || locations.pending} className="min-w-0 space-y-3">
        {form.paths.map((path, i) => (
          <div key={i} className="flex items-center gap-2">
            <PathAutocompleteInput
              value={path}
              onValueChange={(value) => form.updatePath(i, value)}
              placeholder="Filesystem folder"
              disabled={!canFilesystem}
            />
            <Button
              type="button"
              variant="ghost"
              size="sm"
              aria-label={`Remove filesystem folder ${i + 1}`}
              onClick={() => form.removePath(i)}
            >
              Remove
            </Button>
          </div>
        ))}
        {locations.selections.map((root, i) => {
          const savedSource = locations.sources.find((s) => s.source_key === root.source_key);
          return (
            <div
              key={i}
              className="border-border flex items-center gap-2 rounded-md border p-2 text-sm"
            >
              <div className="min-w-0 flex-1">
                <p className="truncate">{locationRootName(root, savedSource)}</p>
                <p className="text-muted-foreground truncate text-xs">
                  {savedSource ? storageSourceLabel(savedSource) : "Storage source"} ·{" "}
                  {root.logical_path}
                </p>
                {(!root.selection_ref ||
                  (savedSource && (!savedSource.enabled || !savedSource.configured))) && (
                  <p className="text-warning text-xs">
                    Unavailable — remove this folder or reload when its source is available.
                  </p>
                )}
              </div>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                aria-label={`Remove ${root.name || "root"}`}
                onClick={() => locations.removeSelection(i)}
              >
                Remove
              </Button>
            </div>
          );
        })}
        {form.errors.paths && <p className="text-destructive text-xs">{form.errors.paths}</p>}
        {canFilesystem && (
          <div className="flex gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setFilesystemOpen(true)}
            >
              Browse folders
            </Button>
            <Button type="button" variant="outline" size="sm" onClick={form.addPath}>
              Add Path
            </Button>
          </div>
        )}
        {usable.length > 0 && (
          <div className="flex flex-col gap-2 sm:flex-row">
            <Select value={sourceKey} onValueChange={setSourceKey}>
              <SelectTrigger aria-label="Choose a storage source" className="min-w-0 flex-1">
                <SelectValue placeholder="Choose a storage source" />
              </SelectTrigger>
              <SelectContent>
                {usable.map((s) => (
                  <SelectItem key={s.source_key} value={s.source_key}>
                    {storageSourceLabel(s)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={!source}
              onClick={() => setStorageOpen(true)}
            >
              Browse source folders
            </Button>
          </div>
        )}
        {!mixing && (
          <p className="text-muted-foreground text-xs">
            This server supports filesystem folders or one storage source per library.
          </p>
        )}
      </fieldset>
      <FolderBrowser
        open={filesystemOpen && locations.ready}
        onOpenChange={setFilesystemOpen}
        existingPaths={paths}
        onSelect={(selected) => {
          if (locations.isCurrent()) {
            form.mergeBrowsedPaths(selected);
            setFilesystemOpen(false);
          }
        }}
      />
      {source && locations.api && (
        <StorageFolderBrowser
          open={storageOpen && locations.ready}
          onOpenChange={setStorageOpen}
          api={locations.api}
          source={source}
          onSelect={(roots) => {
            if (locations.isCurrent()) locations.addSelections(roots);
          }}
        />
      )}
    </div>
  );
}
