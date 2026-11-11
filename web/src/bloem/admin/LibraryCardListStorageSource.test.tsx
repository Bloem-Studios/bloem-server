import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { Library } from "@/api/types";
import { bloemEnglishText } from "@/bloem/i18n";
import { LibraryCardList } from "@/components/admin/libraries/LibraryCardList";
import type { LibraryRow } from "@/components/admin/libraries/useLibraryRows";

// Bloem adds its storage-source label to Silo's phone library cards; this
// guards that seam line in LibraryCardList.tsx across upstream merges.

function row(library: Partial<Library> & Pick<Library, "id" | "name">): LibraryRow {
  return {
    library: {
      type: "ebooks",
      paths: [],
      enabled: true,
      realtime_monitoring: false,
      last_scanned_at: null,
      ...library,
    } as Library,
    activeScans: [],
    runningScanCount: 0,
    queuedScanCount: 0,
    activeRefreshJob: undefined,
    hasActiveWork: false,
    hasScanWarning: false,
    scan: { stop: false, pending: false, disabled: false, label: "Scan", run: vi.fn() },
    refresh: { stop: false, disabled: false, label: "Refresh", run: vi.fn() },
    mountCheck: { pending: false, result: undefined, message: undefined, run: vi.fn() },
    cleanup: { available: false, pending: false, run: vi.fn() },
    cancellingJobID: undefined,
    cancellingScans: false,
    cancelJob: vi.fn(),
    cancelScans: vi.fn(),
    edit: vi.fn(),
    remove: vi.fn(),
  };
}

describe("LibraryCardList storage source label", () => {
  it("labels only the card whose library reads a storage source", () => {
    render(
      <LibraryCardList
        rows={[
          row({ id: 1, name: "Bucket books", storage_source: "s3-books" }),
          row({ id: 2, name: "Folder books", paths: ["/media/books"] }),
        ]}
        realtimeMonitoringByLibraryId={new Map()}
        trickplayByLibraryId={new Map()}
        onMove={vi.fn()}
        moving={false}
      />,
    );

    const label = bloemEnglishText("bloem.admin.libraries.form.storageSource");
    expect(screen.getAllByText(label)).toHaveLength(1);

    const bucketCard = screen.getByRole("heading", { name: "Bucket books" }).closest("article");
    const folderCard = screen.getByRole("heading", { name: "Folder books" }).closest("article");
    expect(within(bucketCard!).getByText(label)).toBeInTheDocument();
    expect(within(folderCard!).queryByText(label)).not.toBeInTheDocument();
  });
});
