// @vitest-environment jsdom

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import DirectAccountsPage from "./DirectAccountsPage";

vi.mock("@/components/admin/policy/AccountPicker", () => ({
  AccountPicker: ({ label }: { label: string }) => <div>{label}</div>,
}));

vi.mock("./AccountEntitlementPanel", () => ({
  AccountEntitlementPanel: ({ userID }: { userID: string }) => (
    <div>Loaded direct account {userID}</div>
  ),
}));

describe("DirectAccountsPage", () => {
  it("requires an explicit account ID before mounting its entitlement controls", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <DirectAccountsPage />
      </MemoryRouter>,
    );

    expect(screen.queryByText(/Loaded direct account/)).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("Or enter an account number"), "42");
    await user.click(screen.getByRole("button", { name: "Open account" }));

    expect(screen.getByText("Loaded direct account 42")).toBeInTheDocument();
  });

  it("links to the explicit bulk policy workflow", () => {
    render(
      <MemoryRouter>
        <DirectAccountsPage />
      </MemoryRouter>,
    );

    expect(screen.getByRole("link", { name: "Change many accounts" })).toHaveAttribute(
      "href",
      "/admin/platform/direct-accounts/bulk",
    );
  });
});
