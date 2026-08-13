// @vitest-environment jsdom

import { cleanup, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { renderWithPolicyProviders } from "./policyTestUtils";
import AdminPolicyLayout from "./AdminPolicyLayout";

vi.mock("@/contexts/AdminContextProvider", () => ({
  useAdminContext: () => ({
    active: { key: "organization:org-1", scope: "organization", name: "North Sea Media" },
  }),
}));

describe("AdminPolicyLayout authority boundary", () => {
  afterEach(cleanup);

  it("does not load or render Rego management in organization context", () => {
    const fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal("fetch", fetchMock);

    renderWithPolicyProviders(<AdminPolicyLayout />);

    expect(screen.getByRole("alert")).toHaveTextContent(/only in Platform context/i);
    expect(screen.queryByRole("tab", { name: /Overrides|Baseline/ })).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
    vi.unstubAllGlobals();
  });
});
