import { afterEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";

const profile = vi.hoisted(() => ({ id: "profile-a" }));
vi.mock("@/hooks/useCurrentProfile", () => ({
  useCurrentProfile: () => ({ profile }),
}));

import { useRememberedPresentation } from "./presentationChoice";

afterEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
  profile.id = "profile-a";
});

describe("presentation choice when browser storage is unavailable", () => {
  it("keeps an explicit recording choice on the page", () => {
    vi.spyOn(localStorage, "setItem").mockImplementation(() => {
      throw new DOMException("Storage full", "QuotaExceededError");
    });
    const { result } = renderHook(() => useRememberedPresentation("book-a"));
    act(() => result.current[1]("group:local-a"));
    expect(result.current[0]).toBe("group:local-a");
  });

  it("does not carry the page choice into another profile or book", () => {
    vi.spyOn(localStorage, "setItem").mockImplementation(() => {
      throw new DOMException("Storage full", "QuotaExceededError");
    });
    const { result, rerender } = renderHook(({ book }) => useRememberedPresentation(book), {
      initialProps: { book: "book-a" },
    });
    act(() => result.current[1]("group:local-a"));
    expect(result.current[0]).toBe("group:local-a");
    profile.id = "profile-b";
    rerender({ book: "book-a" });
    expect(result.current[0]).toBeNull();
    act(() => result.current[1]("group:local-b"));
    rerender({ book: "book-b" });
    expect(result.current[0]).toBeNull();
  });
});
