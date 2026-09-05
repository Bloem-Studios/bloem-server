import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import { LiveTVAccessGate } from "./LiveTVAccessGate";

let state = {
  data: undefined as undefined | { supported: boolean; allowed: boolean; available: boolean },
  isError: false,
  error: null as null | { status: number },
  refetch: vi.fn(),
};
vi.mock("@/hooks/queries/useLiveTVAccess", () => ({ useLiveTVAccess: () => state }));
function renderGate() {
  return renderToStaticMarkup(
    <MemoryRouter>
      <LiveTVAccessGate>
        <div>Protected guide</div>
      </LiveTVAccessGate>
    </MemoryRouter>,
  );
}
describe("Live TV route access", () => {
  it("does not mount the guide before access resolves", () => {
    state = { ...state, data: undefined, isError: false };
    expect(renderGate()).not.toContain("Protected guide");
  });
  it("does not mount the guide for denied viewers", () => {
    state = {
      ...state,
      data: { supported: true, allowed: false, available: false },
      isError: false,
    };
    expect(renderGate()).not.toContain("Protected guide");
  });
  it("does not retain protected content after a failed permission refresh", () => {
    state = {
      ...state,
      data: { supported: true, allowed: true, available: true },
      isError: true,
      error: { status: 503 },
    };
    const html = renderGate();
    expect(html).not.toContain("Protected guide");
    expect(html).toContain("Try again");
  });
  it("allows an empty lineup so the viewer can still access recordings", () => {
    state = {
      ...state,
      data: { supported: true, allowed: true, available: false },
      isError: false,
    };
    expect(renderGate()).toContain("Protected guide");
  });
});
