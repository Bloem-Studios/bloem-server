import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { setAccessToken, setProfileId, setProfileToken } from "@/api/client";
import { adminSubtitleListScope } from "@/api/v2/adminSubtitles";
import { IntegrationsStep } from "./IntegrationsStep";
const mocks = vi.hoisted(() => ({ save: vi.fn() }));
vi.mock("../WizardContext", () => ({ useWizardContext: () => ({ markDone: vi.fn() }) }));
vi.mock("@/hooks/queries/admin/subtitles", () => ({
  useSubtitleProviders: () => ({
    scope: adminSubtitleListScope(),
    isLoading: false,
    data: {
      providers: [
        { provider_name: "subdl", enabled: true, has_api_key: true, has_credentials: false },
      ],
    },
  }),
  useUpdateSubtitleProvider: () => ({ mutate: mocks.save, isPending: false }),
  useTestSubtitleProvider: () => ({ mutate: vi.fn(), isPending: false }),
}));
beforeEach(() => {
  mocks.save.mockReset();
  setAccessToken("admin");
  setProfileId("owner");
  setProfileToken(null);
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          provider_name: "subdl",
          enabled: true,
          has_api_key: true,
          has_credentials: false,
        }),
        { headers: { "Content-Type": "application/json", ETag: '"original"' } },
      ),
    ),
  );
});
afterEach(() => vi.unstubAllGlobals());
it("loads canonical setup editor before changes and saves disabled using its captured validator", async () => {
  const user = userEvent.setup();
  render(<IntegrationsStep />);
  expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "Configure SubDL" }));
  await waitFor(() => expect(screen.getByRole("switch", { name: "Enable SubDL" })).toBeEnabled());
  await user.click(screen.getByRole("switch", { name: "Enable SubDL" }));
  await user.click(screen.getByRole("button", { name: "Save" }));
  expect(mocks.save).toHaveBeenCalledWith(
    {
      editor: expect.objectContaining({ etag: '"original"' }),
      config: { enabled: false, api_key: "" },
    },
    expect.anything(),
  );
  expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
});
it("retains setup draft on uncertain write and displays confirmed save separately from local apply", async () => {
  const user = userEvent.setup();
  mocks.save.mockImplementation((_input, callbacks) => callbacks.onError(new Error("uncertain")));
  render(<IntegrationsStep />);
  await user.click(screen.getByRole("button", { name: "Configure SubDL" }));
  await user.type(screen.getByPlaceholderText("Leave blank to keep"), "draft-secret");
  await user.click(screen.getByRole("button", { name: "Save" }));
  expect(screen.getByPlaceholderText("Leave blank to keep")).toHaveValue("draft-secret");
  expect(screen.getByText(/Save not confirmed/)).toBeInTheDocument();
  expect(mocks.save).toHaveBeenCalledTimes(1);
  // Reload is an explicit discard. Supply a fresh Response for the second read.
  vi.mocked(fetch).mockResolvedValue(
    new Response(
      JSON.stringify({
        provider_name: "subdl",
        enabled: true,
        has_api_key: true,
        has_credentials: false,
      }),
      { headers: { "Content-Type": "application/json", ETag: '"next"' } },
    ),
  );
  await user.click(
    screen.getByRole("button", { name: "Reload saved configuration (discard draft)" }),
  );
  await waitFor(() => expect(screen.getByRole("button", { name: "Save" })).toBeEnabled());
  mocks.save.mockImplementation((_input, callbacks) =>
    callbacks.onSuccess({ saved_revision: "6", local_apply: "failed" }),
  );
  await user.click(screen.getByRole("button", { name: "Save" }));
  expect(screen.getByText(/Settings saved, but not applied/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
});
