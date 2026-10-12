import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import type { ItemDetail } from "@/api/types";
import { V2ProblemError } from "@/api/v2/request";
import type { AudiobookFile } from "@/lib/audiobooks/types";
import {
  F1_LOCAL_ID,
  F1_NATIVE_ID,
  F1_WIRE,
  F2_WIRE,
  F4_WIRE,
  F7_CAPABILITIES,
  fixtureItem,
} from "./fixtures";
import { presentationChoiceKey } from "./presentationChoice";

const mocks = vi.hoisted(() => ({
  query: {} as {
    data?: ItemDetail;
    isLoading?: boolean;
    isFetching?: boolean;
    error?: unknown;
  },
  capabilityCalls: [] as boolean[],
  capabilities: { data: undefined as { feature_tokens: string[] } | undefined, isPending: false },
  profileId: "profile-a",
  controller: null as null | {
    active: null;
    activeRequest: null | { contentId: string; files: AudiobookFile[] };
    isBackgroundBarVisible: boolean;
    startPlayback: ReturnType<typeof vi.fn>;
    stopPlayback: ReturnType<typeof vi.fn>;
    toggleActivePlayback: ReturnType<typeof vi.fn>;
  },
}));

vi.mock("@/hooks/queries/catalogRead", () => ({
  useCatalogItemDetail: () => ({
    isLoading: false,
    isFetching: false,
    error: null,
    refetch: vi.fn(),
    ...mocks.query,
  }),
}));
vi.mock("@/hooks/queries/useBloemCapabilities", () => ({
  useBloemCapabilities: (enabled: boolean) => {
    mocks.capabilityCalls.push(enabled);
    return enabled ? mocks.capabilities : { data: undefined, isPending: true };
  },
}));
vi.mock("@/hooks/useCurrentProfile", () => ({
  useCurrentProfile: () => ({ profile: { id: mocks.profileId } }),
}));
vi.mock("@/pages/audiobooks/player/audiobookPlaybackContext", () => ({
  useAudiobookPlaybackController: () => mocks.controller,
}));
vi.mock("@/pages/ItemDetail/useThemeMusic", () => ({ useThemeMusic: () => undefined }));
vi.mock("@/components/AddToCollectionDialog", () => ({ default: () => null }));
vi.mock("@/pages/audiobooks/components/NarratorCard", () => ({ NarratorCard: () => null }));
vi.mock("@/pages/audiobooks/components/NarratorPicker", () => ({ NarratorPicker: () => null }));
vi.mock("@/pages/audiobooks/components/RelatedRail", () => ({ RelatedRail: () => null }));
vi.mock("@/pages/audiobooks/components/ChaptersSection", () => ({
  ChaptersSection: ({ files }: { files: AudiobookFile[] }) => (
    <ol aria-label="Chapters">
      {files.flatMap((file) =>
        (file.chapters ?? []).map((chapter) => (
          <li key={`${file.id}:${chapter.index}`}>{chapter.title}</li>
        )),
      )}
    </ol>
  ),
}));
vi.mock("@/pages/ItemDetail/DetailHero", () => ({
  default: ({ actions, metadata }: { actions?: ReactNode; metadata?: ReactNode }) => (
    <section>
      {metadata}
      {actions}
    </section>
  ),
}));
vi.mock("@/pages/ItemDetail/components/MetadataBadges", () => ({
  default: ({ duration }: { duration?: string }) => <p>Length {duration}</p>,
}));

import BloemAudiobookItemRoute from "./BloemAudiobookItemRoute";

function renderRoute(path = "/item/7301928374650001") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route
          path="/item/:id"
          element={<BloemAudiobookItemRoute itemDetail={<div data-testid="silo-item-detail" />} />}
        />
      </Routes>
    </MemoryRouter>,
  );
}

function startedFileIds() {
  const input = mocks.controller!.startPlayback.mock.lastCall?.[0] as {
    files: AudiobookFile[];
    initialPositionSeconds: number;
  };
  return { ids: input.files.map((file) => file.id), at: input.initialPositionSeconds };
}

beforeEach(() => {
  localStorage.clear();
  // Cold resume would otherwise back up ten seconds.
  localStorage.setItem("audiobook-smart-rewind", "false");
  mocks.query = { data: fixtureItem(F1_WIRE) };
  mocks.capabilityCalls = [];
  mocks.capabilities = { data: F7_CAPABILITIES, isPending: false };
  mocks.profileId = "profile-a";
  mocks.controller = {
    active: null,
    activeRequest: null,
    isBackgroundBarVisible: false,
    startPlayback: vi.fn(),
    stopPlayback: vi.fn(),
    toggleActivePlayback: vi.fn(),
  };
});

afterEach(() => {
  localStorage.clear();
});

describe("BloemAudiobookItemRoute", () => {
  it("resumes the chosen presentation only, at the book position", async () => {
    renderRoute();

    expect(screen.queryByTestId("silo-item-detail")).toBeNull();
    const picker = screen.getByRole("radiogroup", { name: "Recording" });
    expect(within(picker).getByRole("radio", { name: /Remote copy \(beta\)/ })).toHaveAttribute(
      "data-state",
      "checked",
    );
    expect(screen.getByText("Length 2m 30s")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /Resume/ }));
    expect(startedFileIds()).toEqual({ ids: [601, 602], at: 100 });
  });

  it("plays only the local pair once the listener picks it, and remembers the pick", async () => {
    renderRoute();

    await userEvent.click(screen.getByRole("radio", { name: /Local copy/ }));
    expect(screen.getByText("Length 3m 30s")).toBeInTheDocument();
    expect(
      within(screen.getByRole("list", { name: "Chapters" }))
        .getAllByRole("listitem")
        .map((entry) => entry.textContent),
    ).toEqual(["01 - Opening", "02 - Middle"]);

    await userEvent.click(screen.getByRole("button", { name: /Resume/ }));
    expect(startedFileIds()).toEqual({ ids: [501, 502], at: 100 });
    expect(
      localStorage.getItem(
        presentationChoiceKey({
          server: window.location.origin,
          profileId: "profile-a",
          contentId: "7301928374650001",
        }),
      ),
    ).toBe(F1_LOCAL_ID);
  });

  it("keeps the remembered choice per profile", () => {
    localStorage.setItem(
      presentationChoiceKey({
        server: window.location.origin,
        profileId: "profile-b",
        contentId: "7301928374650001",
      }),
      F1_LOCAL_ID,
    );
    const view = renderRoute();
    expect(screen.getByRole("radio", { name: /Remote copy/ })).toHaveAttribute(
      "data-state",
      "checked",
    );

    mocks.profileId = "profile-b";
    view.rerender(
      <MemoryRouter initialEntries={["/item/7301928374650001"]}>
        <Routes>
          <Route
            path="/item/:id"
            element={
              <BloemAudiobookItemRoute itemDetail={<div data-testid="silo-item-detail" />} />
            }
          />
        </Routes>
      </MemoryRouter>,
    );
    expect(screen.getByRole("radio", { name: /Local copy/ })).toHaveAttribute(
      "data-state",
      "checked",
    );
  });

  it("auto-starts ?play=1 with the chosen presentation", () => {
    renderRoute("/item/7301928374650001?play=1");
    expect(startedFileIds()).toEqual({ ids: [601, 602], at: 100 });
  });

  it("marks native unavailable without the capability token and defaults to local", () => {
    mocks.capabilities = { data: { feature_tokens: [] }, isPending: false };
    renderRoute();

    const native = screen.getByRole("radio", { name: /Remote copy \(beta\)/ });
    expect(native).toBeDisabled();
    expect(native).toHaveTextContent(/Not available on this server/);
    expect(screen.getByRole("radio", { name: /Local copy/ })).toHaveAttribute(
      "data-state",
      "checked",
    );
  });

  it("waits for the capability answer before choosing, so ?play=1 never starts the wrong copy", () => {
    mocks.capabilities = { data: undefined, isPending: true };
    renderRoute("/item/7301928374650001?play=1");

    expect(mocks.controller!.startPlayback).not.toHaveBeenCalled();
    expect(screen.queryByRole("radiogroup")).toBeNull();
    expect(screen.queryByTestId("silo-item-detail")).toBeNull();
  });

  it("stops this book's playback when the listener switches recordings", async () => {
    const item = fixtureItem(F1_WIRE);
    mocks.controller!.activeRequest = {
      contentId: item.content_id,
      files: [601, 602].map((id) => ({ id, duration_seconds: 1 })),
    };
    renderRoute();

    expect(screen.getByRole("radio", { name: /Remote copy/ })).toHaveAttribute(
      "data-state",
      "checked",
    );
    await userEvent.click(screen.getByRole("radio", { name: /Local copy/ }));
    expect(mocks.controller!.stopPlayback).toHaveBeenCalledTimes(1);
  });

  it("shows single-track presentations and their own length (F2)", async () => {
    mocks.query = { data: fixtureItem(F2_WIRE) };
    renderRoute("/item/7301928374650002");

    expect(screen.getByRole("radio", { name: /Local copy/ })).toHaveAttribute(
      "data-state",
      "checked",
    );
    expect(screen.getByText("Length 2s")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Listen/ }));
    expect(startedFileIds().ids).toEqual([701]);
  });

  it("offers no download action for a native presentation", () => {
    renderRoute();
    expect(screen.queryByRole("button", { name: /download/i })).toBeNull();
    expect(screen.queryByRole("link", { name: /download/i })).toBeNull();
  });

  it("never shows a native file name", () => {
    renderRoute();
    expect(document.body.textContent).not.toContain("bloem-storage:");
  });

  it("delegates movies with multipart alternatives to the Silo page (F4)", () => {
    mocks.query = { data: fixtureItem(F4_WIRE) };
    renderRoute("/item/m-1");
    expect(screen.getByTestId("silo-item-detail")).toBeInTheDocument();
    expect(mocks.capabilityCalls.every((enabled) => !enabled)).toBe(true);
  });

  it("delegates an ordinary local audiobook to the Silo page", () => {
    const item = fixtureItem(F1_WIRE);
    mocks.query = { data: { ...item, versions: item.versions.slice(0, 2) } };
    renderRoute();
    expect(screen.getByTestId("silo-item-detail")).toBeInTheDocument();
  });

  it.each([
    ["loading", { data: undefined, isLoading: true }],
    [
      "not found",
      {
        data: fixtureItem(F1_WIRE),
        error: new V2ProblemError("getCatalogItem", {
          type: "https://silo.dev/problems/not_found",
          title: "Not found",
          status: 404,
          detail: "Item not found",
          instance: "/api/v2/catalog/items/7301928374650001",
        }),
      },
    ],
    ["failed", { data: undefined, error: new Error("boom") }],
  ])("delegates the %s state to the Silo page", (_, query) => {
    mocks.query = query as typeof mocks.query;
    renderRoute();
    expect(screen.getByTestId("silo-item-detail")).toBeInTheDocument();
  });

  it("does not mutate the cached detail payload", async () => {
    // fixtureItem is deeply frozen: a write anywhere would throw in strict mode.
    const item = fixtureItem(F1_WIRE);
    const before = JSON.stringify(item);
    mocks.query = { data: item };
    renderRoute();
    await act(async () => {
      await userEvent.click(screen.getByRole("radio", { name: /Local copy/ }));
    });
    expect(JSON.stringify(item)).toBe(before);
    expect(F1_NATIVE_ID).toContain("bloem-storage:");
  });
});
