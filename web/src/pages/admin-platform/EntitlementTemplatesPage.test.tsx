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
      if (init?.method === "GET" || !init) return { templates: [standard] } as never;
      return {} as never;
    });
    renderWithQuery(<OrganizationEntitlementPanel organizationID="org-1" />);

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
  });
});
