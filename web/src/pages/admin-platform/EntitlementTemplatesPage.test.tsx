// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";

import { adminV2Api } from "@/api/adminV2Client";
import { EntitlementTemplateEditor } from "./EntitlementTemplateEditor";
import EntitlementTemplatesPage from "./EntitlementTemplatesPage";
import { OrganizationEntitlementPanel } from "./OrganizationEntitlementPanel";
import { AccountEntitlementPanel } from "./AccountEntitlementPanel";

vi.mock("@/api/adminV2Client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/adminV2Client")>();
  return { ...actual, adminV2Api: vi.fn() };
});

const standard = {
  key: "standard",
  name: "Standard",
  revision: 3,
  enabled: true,
  archived: false,
  created_at: "2026-08-21T12:00:00Z",
  policy: {
    library_ids: null,
    playback_allowed: true,
    max_streams: 3,
    max_profiles: 5,
    transcode_allowed: true,
    max_transcodes: 1,
    download_allowed: true,
    download_transcode_allowed: true,
    max_playback_quality: "original",
    requests_allowed: true,
    allowed_permissions: null,
  },
};

function renderWithQuery(node: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>{node}</MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("EntitlementTemplateEditor", () => {
  afterEach(() => cleanup());

  it("supports select all then individual library removal", async () => {
    const user = userEvent.setup();
    const save = vi.fn();
    render(
      <EntitlementTemplateEditor
        libraries={[
          { id: 1, name: "Movies" },
          { id: 2, name: "Series" },
        ]}
        onSave={save}
      />,
    );

    await user.type(screen.getByLabelText("Key"), "custom");
    await user.click(screen.getByRole("button", { name: "Select all libraries" }));
    await user.click(screen.getByRole("checkbox", { name: "Movies" }));
    await user.click(screen.getByRole("button", { name: "Save template" }));

    expect(save).toHaveBeenCalledWith(
      expect.objectContaining({ policy: expect.objectContaining({ library_ids: [2] }) }),
    );
  });

  it("persists selecting all libraries as the dynamic all-enabled policy", async () => {
    const user = userEvent.setup();
    const save = vi.fn();
    render(
      <EntitlementTemplateEditor
        libraries={[
          { id: 1, name: "Movies" },
          { id: 2, name: "Series" },
        ]}
        onSave={save}
      />,
    );

    await user.type(screen.getByLabelText("Key"), "custom");
    await user.click(screen.getByRole("button", { name: "Select all libraries" }));
    await user.click(screen.getByRole("button", { name: "Save template" }));

    expect(save).toHaveBeenCalledWith(
      expect.objectContaining({ policy: expect.objectContaining({ library_ids: null }) }),
    );
  });

  it("turns off transcoded downloads when downloads are disabled", async () => {
    const user = userEvent.setup();
    render(<EntitlementTemplateEditor libraries={[]} onSave={vi.fn()} />);

    await user.click(screen.getByRole("checkbox", { name: "Allow downloads" }));

    expect(screen.getByRole("checkbox", { name: "Allow transcoded downloads" })).toBeDisabled();
    expect(screen.getByRole("checkbox", { name: "Allow transcoded downloads" })).not.toBeChecked();
  });

  it("keeps Browse-only restricted while allowing a metadata revision", async () => {
    const user = userEvent.setup();
    const save = vi.fn();
    render(
      <EntitlementTemplateEditor
        libraries={[]}
        template={{
          key: "browse-only",
          name: "Browse-only",
          revision: 1,
          enabled: true,
          archived: false,
          policy: {
            library_ids: [],
            playback_allowed: false,
            max_streams: 0,
            max_profiles: 0,
            transcode_allowed: false,
            max_transcodes: 0,
            download_allowed: false,
            download_transcode_allowed: false,
            max_playback_quality: "original",
            requests_allowed: false,
            allowed_permissions: null,
          },
        }}
        onSave={save}
      />,
    );

    expect(screen.getByText(/does not permit playback/i)).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "Allow playback" })).toBeDisabled();
    expect(screen.getByLabelText("Name")).not.toBeDisabled();
    expect(screen.getByRole("button", { name: "Save template" })).toBeEnabled();

    await user.clear(screen.getByLabelText("Name"));
    await user.type(screen.getByLabelText("Name"), "Browse media");
    await user.click(screen.getByRole("button", { name: "Save template" }));

    expect(save).toHaveBeenCalledWith(
      expect.objectContaining({
        name: "Browse media",
        policy: expect.objectContaining({
          playback_allowed: false,
          download_allowed: false,
          download_transcode_allowed: false,
        }),
      }),
    );
  });

  it("requires an explicit key when creating a template", async () => {
    const user = userEvent.setup();
    render(<EntitlementTemplateEditor libraries={[]} onSave={vi.fn()} />);

    expect(screen.getByRole("button", { name: "Save template" })).toBeDisabled();
    await user.type(screen.getByLabelText("Key"), "event-pass");
    expect(screen.getByRole("button", { name: "Save template" })).toBeEnabled();
  });

  it("saves playback quality and media request controls", async () => {
    const user = userEvent.setup();
    const save = vi.fn();
    render(<EntitlementTemplateEditor libraries={[]} template={standard} onSave={save} />);

    await user.selectOptions(screen.getByLabelText("Maximum playback quality"), "1080p");
    await user.click(screen.getByRole("checkbox", { name: "Allow media requests" }));
    await user.click(screen.getByRole("button", { name: "Save template" }));

    expect(save).toHaveBeenCalledWith(
      expect.objectContaining({
        policy: expect.objectContaining({
          max_playback_quality: "1080p",
          requests_allowed: false,
        }),
      }),
    );
  });

  it("can restrict the access-group permission set", async () => {
    const user = userEvent.setup();
    const save = vi.fn();
    render(<EntitlementTemplateEditor libraries={[]} template={standard} onSave={save} />);

    await user.click(screen.getByRole("checkbox", { name: "Allow all permissions" }));
    await user.click(screen.getByRole("checkbox", { name: "Marker editing" }));
    await user.click(screen.getByRole("button", { name: "Save template" }));

    expect(save).toHaveBeenCalledWith(
      expect.objectContaining({
        policy: expect.objectContaining({ allowed_permissions: ["metadata_curation"] }),
      }),
    );
  });
});

describe("EntitlementTemplatesPage", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("confirms before archiving a template", async () => {
    const user = userEvent.setup();
    vi.mocked(adminV2Api).mockImplementation(async (_path, init) => {
      if (init?.method === "POST") return { template: { ...standard, archived: true } } as never;
      return { templates: [standard] } as never;
    });
    renderWithQuery(<EntitlementTemplatesPage />);

    await user.click(await screen.findByRole("button", { name: "Archive Standard" }));
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(
      vi.mocked(adminV2Api).mock.calls.some(([path]) => String(path).includes("/archive")),
    ).toBe(false);

    await user.click(screen.getByRole("button", { name: "Archive template" }));
    expect(
      vi.mocked(adminV2Api).mock.calls.some(([path]) => String(path).includes("/standard/archive")),
    ).toBe(true);
  });

  it("collects the new key and name before cloning a pinned revision", async () => {
    const user = userEvent.setup();
    vi.mocked(adminV2Api).mockImplementation(async (_path, init) => {
      if (init?.method === "POST") {
        return { template: { ...standard, key: "standard-copy", name: "Standard copy" } } as never;
      }
      return { templates: [standard] } as never;
    });
    renderWithQuery(<EntitlementTemplatesPage />);

    await user.click(await screen.findByRole("button", { name: "Clone Standard" }));
    expect(screen.getByRole("dialog", { name: "Clone Standard" })).toBeInTheDocument();
    await user.type(screen.getByLabelText("New key"), "standard-copy");
    await user.clear(screen.getByLabelText("New name"));
    await user.type(screen.getByLabelText("New name"), "Standard copy");
    await user.click(screen.getByRole("button", { name: "Create clone" }));

    expect(
      vi.mocked(adminV2Api).mock.calls.some(
        ([path, init]) =>
          String(path).endsWith("/standard/clone") &&
          init?.body ===
            JSON.stringify({
              source_revision: 3,
              key: "standard-copy",
              name: "Standard copy",
            }),
      ),
    ).toBe(true);
  });

  it("shows archive failures inside the confirmation dialog", async () => {
    const user = userEvent.setup();
    vi.mocked(adminV2Api).mockImplementation(async (_path, init) => {
      if (init?.method === "POST") throw new Error("Template is still in use.");
      return { templates: [standard] } as never;
    });
    renderWithQuery(<EntitlementTemplatesPage />);

    await user.click(await screen.findByRole("button", { name: "Archive Standard" }));
    await user.click(screen.getByRole("button", { name: "Archive template" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Template is still in use.");
  });

  it("shows revision policy details and rolls history forward as a new revision", async () => {
    const user = userEvent.setup();
    const oldRevision = {
      ...standard,
      revision: 1,
      created_at: "2026-08-20T10:30:00Z",
      policy: { ...standard.policy, max_streams: 1, requests_allowed: false },
    };
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      const url = String(path);
      if (url.endsWith("/history")) return { revisions: [standard, oldRevision] } as never;
      if (init?.method === "POST") return { template: { ...oldRevision, revision: 4 } } as never;
      return { templates: [standard] } as never;
    });
    renderWithQuery(<EntitlementTemplatesPage />);

    await user.click(await screen.findByRole("button", { name: "Edit Standard" }));
    expect(await screen.findByText(/1 stream · 5 profiles/)).toBeInTheDocument();
    expect(screen.getByText(/Requests off/)).toBeInTheDocument();
    expect(screen.getByText(/Aug 20/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Roll back to revision 1" }));

    expect(
      vi.mocked(adminV2Api).mock.calls.some(
        ([path, init]) =>
          String(path).endsWith("/standard/revisions") &&
          init?.body ===
            JSON.stringify({
              expected_revision: 3,
              source_revision: 1,
              name: "Standard",
              enabled: true,
            }),
      ),
    ).toBe(true);
  });
});

describe("OrganizationEntitlementPanel", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("requires a dry run and explicit confirmation before applying", async () => {
    const user = userEvent.setup();
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      const url = String(path);
      if (url.includes("/dry-run")) {
        return {
          template_key: "standard",
          template_revision: 3,
          changed: true,
          dry_run_token: "dry-run-token",
          expires_at: "2026-08-21T18:00:00Z",
          changes: [{ field: "max_streams", before: 1, after: 3 }],
          warnings: [],
        } as never;
      }
      if (url.endsWith("/apply"))
        return { template_key: "standard", template_revision: 3, changed: true } as never;
      if (url.endsWith("/organizations/org-1/entitlement")) {
        return {
          template_key: "standard",
          template_revision: 2,
          managed_default_group: {
            id: "managed-1",
            name: "Members",
            policy: { ...standard.policy, max_streams: 2 },
          },
          tenant_limits: { slots: 25, transcodes: 4 },
          library_ids: [8, 9],
          last_reconciled_at: "2026-08-21T14:15:00Z",
          audit_history_href: "https://attacker.example/audit",
        } as never;
      }
      if (init?.method === "GET" || !init) return { templates: [standard] } as never;
      return {} as never;
    });
    renderWithQuery(<OrganizationEntitlementPanel organizationID="org-1" />);

    expect(await screen.findByText("standard · revision 2")).toBeInTheDocument();
    expect(screen.getByText("25 slots · 4 transcodes")).toBeInTheDocument();
    expect(screen.getByText("Libraries 8, 9")).toBeInTheDocument();
    expect(screen.getByText(/2 streams · 5 profiles/)).toBeInTheDocument();
    expect(screen.getByText(/Aug 21/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "View apply history" })).toHaveAttribute(
      "href",
      "/admin/platform/activity?organization_id=org-1&event=entitlement",
    );

    await user.click(await screen.findByRole("button", { name: "Preview changes" }));
    expect(await screen.findByText("max_streams")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Apply to existing tenant" })).toBeDisabled();

    await user.click(screen.getByRole("checkbox", { name: /I understand/i }));
    await user.click(screen.getByRole("button", { name: "Apply to existing tenant" }));
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(vi.mocked(adminV2Api).mock.calls.some(([path]) => String(path).endsWith("/apply"))).toBe(
      false,
    );

    await user.click(screen.getByRole("button", { name: "Confirm apply" }));
    expect(vi.mocked(adminV2Api).mock.calls.some(([path]) => String(path).endsWith("/apply"))).toBe(
      true,
    );
    expect(screen.queryByText("max_streams")).not.toBeInTheDocument();
  });
});

describe("AccountEntitlementPanel", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("previews and applies a direct account entitlement without changing custom groups", async () => {
    const user = userEvent.setup();
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      const url = String(path);
      if (url.endsWith("/users/42/entitlement/dry-run")) {
        return {
          template_key: "standard",
          template_revision: 3,
          changed: true,
          dry_run_token: "account-token",
          expires_at: "2026-08-21T18:00:00Z",
          changes: [{ field: "max_streams", before: 1, after: 3 }],
          warnings: [],
        } as never;
      }
      if (url.endsWith("/users/42/entitlement/apply")) {
        return { template_key: "standard", template_revision: 3, changed: true } as never;
      }
      if (url.endsWith("/users/42/entitlement")) {
        return {
          template_key: "standard",
          template_revision: 2,
          managed_default_group: { id: "group-1", name: "Direct members", policy: standard.policy },
          library_ids: [8],
          last_reconciled_at: "2026-08-21T14:15:00Z",
        } as never;
      }
      return { templates: [standard] } as never;
    });

    renderWithQuery(<AccountEntitlementPanel userID="42" />);

    expect(await screen.findByText("standard · revision 2")).toBeInTheDocument();
    expect(screen.getByText(/Custom profile groups are never modified/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Preview account changes" }));
    await user.click(await screen.findByRole("checkbox", { name: /managed account group/i }));
    await user.click(screen.getByRole("button", { name: "Apply to account" }));
    await user.click(screen.getByRole("button", { name: "Confirm account apply" }));

    expect(
      vi
        .mocked(adminV2Api)
        .mock.calls.some(([path]) => String(path).endsWith("/users/42/entitlement/apply")),
    ).toBe(true);
    expect(screen.queryByText("max_streams")).not.toBeInTheDocument();
  });
});
