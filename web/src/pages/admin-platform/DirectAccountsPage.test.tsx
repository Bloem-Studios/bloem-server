// @vitest-environment jsdom

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import DirectAccountsPage from "./DirectAccountsPage";

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
    await user.type(screen.getByLabelText("Account ID"), "42");
    await user.click(screen.getByRole("button", { name: "Load account" }));

    expect(screen.getByText("Loaded direct account 42")).toBeInTheDocument();
  });
});
