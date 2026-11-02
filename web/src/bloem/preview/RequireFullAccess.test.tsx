import { act, render, screen } from "@testing-library/react";
import { Suspense } from "react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { PreviewRouteBoundary } from "./RequireFullAccess";
import { setGuestHost, setPreviewDiscoveryPending } from "./session";

const auth = vi.hoisted(() => ({ user: null as { id: string } | null }));
vi.mock("@/hooks/useAuth", () => ({ useAuth: () => auth }));
vi.mock("./PreviewLanding", () => ({ default: () => <div>preview landing</div> }));

beforeEach(() => {
  sessionStorage.clear();
  auth.user = null;
  setGuestHost(false);
  setPreviewDiscoveryPending(false);
});
afterEach(() => {
  setPreviewDiscoveryPending(false);
  setGuestHost(false);
});

function renderBoundary(path = "/") {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Suspense fallback={<div>suspended</div>}>
        <PreviewRouteBoundary>
          <div>app routes</div>
        </PreviewRouteBoundary>
      </Suspense>
    </MemoryRouter>,
  );
}

// A signed-out visitor must not reach the app's routes (which send them to
// sign in) before the server says whether this host is a preview host.
it("holds a signed-out visitor until the preview status answers, then shows the landing on a preview host", async () => {
  setPreviewDiscoveryPending(true);
  renderBoundary();
  expect(screen.getByRole("status")).toBeTruthy();
  expect(screen.queryByText("app routes")).toBeNull();

  act(() => {
    setGuestHost(true);
    setPreviewDiscoveryPending(false);
  });
  expect(await screen.findByText("preview landing")).toBeTruthy();
  expect(screen.queryByText("app routes")).toBeNull();
});

it("lets a signed-out visitor through once the status says this is not a preview host", () => {
  setPreviewDiscoveryPending(true);
  renderBoundary();
  expect(screen.queryByText("app routes")).toBeNull();

  act(() => setPreviewDiscoveryPending(false));
  expect(screen.getByText("app routes")).toBeTruthy();
});

it("does not hold a signed-in session while the preview status is pending", () => {
  auth.user = { id: "7" };
  setPreviewDiscoveryPending(true);
  renderBoundary();
  expect(screen.getByText("app routes")).toBeTruthy();
});

// The wait only stands in for the sign-in redirect. A public page such as the
// login screen is never held, or it would mount twice and read everything twice.
it.each(["/login", "/signup", "/activate", "/household-setup"])(
  "does not hold the public page %s while the preview status is pending",
  (path) => {
    setPreviewDiscoveryPending(true);
    renderBoundary(path);
    expect(screen.getByText("app routes")).toBeTruthy();
  },
);
