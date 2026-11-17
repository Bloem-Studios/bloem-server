// Bloem: an already-open palette selects its text at once (AdminSectionCommandDialog.tsx),
// so a deferred selection cannot overwrite the first character typed. The
// harness follows Silo's AdminSectionCommandDialog.test.tsx.
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { expect, it, vi } from "vitest";

import { buildAdminCommandNavSections } from "@/lib/adminNavigation";

vi.mock("@/lib/buildPluginHref", () => ({ navigateToPluginRoute: vi.fn() }));

import { AdminSectionCommandDialog } from "./AdminSectionCommandDialog";

it("preserves typing that starts before the next animation frame", async () => {
  const frames: FrameRequestCallback[] = [];
  const frame = vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
    frames.push(callback);
    return frames.length;
  });
  try {
    render(
      <MemoryRouter initialEntries={["/admin"]}>
        <AdminSectionCommandDialog sections={buildAdminCommandNavSections(undefined)} />
      </MemoryRouter>,
    );
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    const searchBox = await screen.findByRole("searchbox", { name: "Search admin sections" });
    await waitFor(() => expect(searchBox).toHaveFocus());
    await userEvent.keyboard("l");
    act(() => frames.splice(0).forEach((callback) => callback(0)));
    await userEvent.keyboard("ogs");
    expect(searchBox).toHaveValue("logs");
    expect(screen.getByRole("option", { name: /^Server LogServer log stream/ })).toBeInTheDocument();
  } finally {
    frame.mockRestore();
  }
});
