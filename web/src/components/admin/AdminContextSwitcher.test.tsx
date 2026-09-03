import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setAccessToken } from "@/api/client";
import { AdminContextProvider } from "@/contexts/AdminContextProvider";
import AdminContextSwitcher from "./AdminContextSwitcher";
import type { User } from "@/api/types";

const platformAdmin: User = {
  id: 1,
  username: "admin",
  email: "admin@example.test",
  role: "admin",
  permissions: [],
  download_allowed: true,
};

describe("AdminContextSwitcher", () => {
  beforeEach(() => {
    window.localStorage.clear();
    window.localStorage.setItem("silo-admin-context", "organization:org-a");
    setAccessToken("account-token");
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input) === "/api/bloem/v1/organizations") {
          return Promise.resolve(
            new Response(
              JSON.stringify({
                organizations: [
                  {
                    id: "org-a",
                    slug: "org-a",
                    name: "Org A",
                    default: true,
                    membership_id: "membership-a",
                    membership_role: "admin",
                    policy_revision: 7,
                    security_revision: 11,
                  },
                ],
              }),
              { status: 200 },
            ),
          );
        }
        const body = JSON.parse(String(init?.body ?? "{}")) as { scope?: string };
        return Promise.resolve(
          new Response(
            JSON.stringify({
              access_token: `token-${body.scope}`,
              expires_at: "2026-08-13T12:15:00Z",
              context:
                body.scope === "platform"
                  ? {
                      key: "platform",
                      scope: "platform",
                      name: "Platform",
                      status: "active",
                      authority: "platform_admin",
                    }
                  : {
                      key: "organization:org-a",
                      scope: "organization",
                      organization_id: "org-a",
                      name: "Org A",
                      status: "active",
                      authority: "platform_admin",
                      policy_revision: 7,
                      security_revision: 11,
                    },
            }),
            { status: 200 },
          ),
        );
      }),
    );
  });

  it("shows the active scope, status and authority and permits keyboard switching", async () => {
    const onSwitchSuccess = vi.fn();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter initialEntries={["/admin/organization"]}>
          <AdminContextProvider user={platformAdmin}>
            <h1 tabIndex={-1}>Page heading</h1>
            <AdminContextSwitcher onSwitchSuccess={onSwitchSuccess} />
          </AdminContextProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    const select = screen.getByRole("combobox", { name: "Administrative context" });
    await waitFor(() => expect(select).toHaveValue("organization:org-a"));
    expect(screen.getByText("organization · active")).toBeInTheDocument();
    expect(screen.getByText("Platform administrator")).toBeInTheDocument();

    await userEvent.selectOptions(select, "platform");
    await waitFor(() => expect(select).toHaveValue("platform"));
    expect(onSwitchSuccess).toHaveBeenCalledOnce();
    expect(screen.getByText("platform · active")).toBeInTheDocument();
  });
});
