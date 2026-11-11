import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { BloemSearchMirrorStatus } from "@/api/bloemSearchMirror";

import { SearchMirrorStatus } from "./SearchMirrorStatus";

const statusMock = vi.fn();
const adminContext = vi.hoisted(() => ({
  value: { active: { scope: "platform", key: "platform" } } as {
    active: { scope: "platform" | "organization"; key: string } | null;
  } | null,
}));

vi.mock("@/hooks/queries/admin/searchMirror", () => ({
  useSearchMirrorStatus: () => statusMock(),
}));
vi.mock("@/contexts/AdminContextProvider", () => ({
  useOptionalAdminContext: () => adminContext.value,
}));

const INDEX = "silo_media_items_rebuild_1762000000";

const FOLLOWING: BloemSearchMirrorStatus = {
  configured: true,
  source: "environment",
  url: "http://m:7700",
  key_source: "file",
  state: "following",
  settling: false,
  settling_overdue: false,
  lag_seconds: 12,
  up_to_date_as_of: "2026-11-11T10:00:00Z",
  pending_events: 3,
  pending_events_capped: false,
  mirror_index: INDEX,
  main_index: INDEX,
  copy: null,
  last_full_copy_at: "2026-11-10T03:00:00Z",
  error_since: null,
  dead_letters: 0,
  max_lag_seconds: 259200,
  role_swaps: 0,
  state_exists: true,
  warnings: [],
};

function answer(data: BloemSearchMirrorStatus | undefined, extra: object = {}) {
  statusMock.mockReturnValue({ data, isLoading: false, isError: false, ...extra });
}

function renderSection() {
  return render(
    <MemoryRouter>
      <SearchMirrorStatus />
    </MemoryRouter>,
  );
}

describe("SearchMirrorStatus", () => {
  beforeEach(() => {
    statusMock.mockReset();
    adminContext.value = { active: { scope: "platform", key: "platform" } };
  });

  it("renders nothing when the mirror is off and has no state", () => {
    answer({
      ...FOLLOWING,
      configured: false,
      state: "off",
      state_exists: false,
      mirror_index: "",
      main_index: "",
      lag_seconds: null,
      up_to_date_as_of: null,
      pending_events: null,
      last_full_copy_at: null,
    });
    const { container } = renderSection();
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing while loading, on error and before any answer", () => {
    statusMock.mockReturnValue({ data: undefined, isLoading: true, isError: false });
    const loading = renderSection();
    expect(loading.container).toBeEmptyDOMElement();
    loading.unmount();

    statusMock.mockReturnValue({ data: FOLLOWING, isLoading: false, isError: true });
    const failed = renderSection();
    expect(failed.container).toBeEmptyDOMElement();
    failed.unmount();

    answer(undefined);
    expect(renderSection().container).toBeEmptyDOMElement();
  });

  it("shows state, lag, both index names and the task links when following", () => {
    answer(FOLLOWING);
    const view = renderSection();

    expect(screen.getByRole("heading", { name: "Search mirror" })).toBeInTheDocument();
    expect(screen.getByText("Following")).toBeInTheDocument();
    expect(screen.getByText("12 seconds behind")).toBeInTheDocument();
    expect(screen.getAllByText(INDEX)).toHaveLength(2);
    expect(screen.getByText("3 rows")).toBeInTheDocument();
    expect(screen.getByText("3 days")).toBeInTheDocument();

    const hrefs = within(view.container)
      .getAllByRole("link")
      .map((link) => link.getAttribute("href"));
    expect(hrefs).toHaveLength(2);
    expect(hrefs).toEqual(
      expect.arrayContaining([
        "/admin/tasks/sync_catalog_search_mirror",
        "/admin/tasks/rebuild_catalog_search_mirror",
      ]),
    );
    // A status section only: no controls of its own.
    expect(view.container.querySelectorAll("input, select, textarea, button")).toHaveLength(0);
  });

  it("shows the one-copy warning", () => {
    answer({
      ...FOLLOWING,
      configured: false,
      warnings: ["configured_on_one_copy_only"],
    });
    renderSection();
    expect(
      screen.getByText(
        "This database has search mirror state, but this server has no mirror configured. Set SILO_MEILISEARCH_MIRROR_URL on every Bloem copy; a copy without it cannot bring the new main index up to date after a failover.",
      ),
    ).toBeInTheDocument();
  });

  it("shows one notice per warning code, and a code it does not know", () => {
    answer({
      ...FOLLOWING,
      warnings: ["settling_overdue", "stood_down", "something_new"],
    });
    renderSection();
    expect(screen.getByText(/settling after a failover for more than 30 minutes/)).toBeVisible();
    expect(
      screen.getByText("Another Bloem copy took over search, so this copy has stopped mirroring."),
    ).toBeVisible();
    expect(screen.getByText("Warning: something_new")).toBeVisible();
  });

  it("shows copy progress while copying", () => {
    answer({
      ...FOLLOWING,
      state: "copying",
      reason: "first_copy",
      mirror_index: "silo_media_items_mirrorcopy_1762000100",
      copy: { done: 500, total: 2000, ratio: 0.25, started_at: "2026-11-11T09:50:00Z" },
    });
    renderSection();
    expect(screen.getByText("Copying")).toBeInTheDocument();
    expect(screen.getByText("first_copy")).toBeInTheDocument();
    expect(screen.getByText("25%")).toBeInTheDocument();
    expect(screen.getByText("500 of 2,000 items")).toBeInTheDocument();
  });

  it("says when the waiting rows are more than the count can show", () => {
    answer({ ...FOLLOWING, pending_events: 1_000_000, pending_events_capped: true });
    renderSection();
    expect(screen.getByText("More than 1,000,000 rows")).toBeInTheDocument();
  });

  it("shows the last error with when it began, and the dead-letters", () => {
    answer({
      ...FOLLOWING,
      state: "unreachable",
      reason: "connection_refused",
      last_error: "dial tcp 10.0.0.9:7700: connect: connection refused",
      error_since: "2026-11-11T09:00:00Z",
      dead_letters: 4,
      lag_seconds: 5400,
    });
    renderSection();
    expect(screen.getByText("Unreachable")).toBeInTheDocument();
    expect(
      screen.getByText("dial tcp 10.0.0.9:7700: connect: connection refused"),
    ).toBeInTheDocument();
    expect(screen.getByText(/^since /)).toBeInTheDocument();
    expect(screen.getByText("4")).toBeInTheDocument();
    expect(screen.getByText("90 minutes behind")).toBeInTheDocument();
  });

  it("shows a mirror that has never been copied or caught up", () => {
    answer({
      ...FOLLOWING,
      state: "needs_full_copy",
      reason: "past_cap",
      lag_seconds: null,
      up_to_date_as_of: null,
      pending_events: null,
      mirror_index: "",
      last_full_copy_at: null,
    });
    renderSection();
    expect(screen.getByText("Needs a full copy")).toBeInTheDocument();
    expect(screen.getByText("Not yet")).toBeInTheDocument();
    expect(screen.getByText("Never")).toBeInTheDocument();
    expect(screen.getByText("Unknown")).toBeInTheDocument();
    expect(screen.getByText("Not built yet")).toBeInTheDocument();
  });

  it("renders nothing outside a platform context", () => {
    // The query is disabled there: no data, and not loading.
    statusMock.mockReturnValue({ data: undefined, isLoading: false, isError: false });
    adminContext.value = { active: { scope: "organization", key: "organization:north" } };
    const organization = renderSection();
    expect(organization.container).toBeEmptyDOMElement();
    organization.unmount();

    adminContext.value = null;
    expect(renderSection().container).toBeEmptyDOMElement();
    // Even a stale answer from the cache is not shown to a non-platform administrator.
    statusMock.mockClear();
    answer(FOLLOWING);
    expect(renderSection().container).toBeEmptyDOMElement();
    expect(statusMock).not.toHaveBeenCalled();
  });
});
