// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { XtreamGuideSourceForm } from "./XtreamGuideSourceForm";
import { XtreamRemoveButton } from "./XtreamRemoveButton";

const fixture = vi.hoisted(() => ({
  generation: 1,
  pin: 1,
  profile: "parent",
  role: "admin",
  pending: false,
  configured: false,
  mutate: vi.fn(),
  refetch: vi.fn(),
}));
vi.mock("@/hooks/useAuth", () => ({ useAuth: () => ({ user: { role: fixture.role } }) }));
vi.mock("@/api/client", async (original) => ({
  ...(await original<typeof import("@/api/client")>()),
  captureSessionIdentity: () => ({
    serverOrigin: "https://server.test",
    authContextVersion: fixture.generation,
  }),
  isSessionIdentityCurrent: (value: { authContextVersion: number }) =>
    value.authContextVersion === fixture.generation,
  getProfileTokenGeneration: () => fixture.pin,
}));
vi.mock("@/api/bloemClient", async (original) => ({
  ...(await original<typeof import("@/api/bloemClient")>()),
  getProfileId: () => fixture.profile,
}));
vi.mock("@/hooks/queries/useLiveTV", () => ({
  useLiveTVTuners: () => ({
    data: [
      { id: "provider", type: "xtream", model: "Fixture TV", base_url: "https://provider.test" },
      { id: "antenna", type: "hdhomerun", model: "Antenna", base_url: "http://192.168.1.20" },
    ],
  }),
  useLiveTVGuideSources: () => ({
    data: fixture.configured
      ? [{ id: "guide", type: "xtream", enabled: true, config: { tuner_id: "provider" } }]
      : [],
    refetch: fixture.refetch,
  }),
  useCreateLiveTVGuideSource: () => ({ isPending: fixture.pending, mutateAsync: fixture.mutate }),
}));
beforeEach(() => {
  fixture.generation++;
  fixture.pin = 1;
  fixture.profile = "parent";
  fixture.role = "admin";
  fixture.pending = false;
  fixture.configured = false;
  fixture.mutate.mockReset().mockResolvedValue({ id: "guide" });
  fixture.refetch.mockReset().mockResolvedValue({ data: [{ id: "guide" }] });
});
afterEach(cleanup);

it("creates a guide using only a configured provider identity, without another credential or URL input", async () => {
  render(<XtreamGuideSourceForm />);
  expect(screen.queryByRole("option", { name: /Antenna/ })).toBeNull();
  fireEvent.change(screen.getByLabelText("Xtream provider"), { target: { value: "provider" } });
  fireEvent.click(screen.getByRole("button", { name: "Add Xtream guide" }));
  expect(fixture.mutate).toHaveBeenCalledTimes(1);
  expect(fixture.mutate.mock.calls[0]?.[0]).toEqual({
    type: "xtream",
    enabled: true,
    priority: 100,
    display_name: "Fixture TV guide",
    config: { tuner_id: "provider" },
  });
  expect(screen.queryByLabelText(/password/i)).toBeNull();
  await waitFor(() =>
    expect(fixture.refetch).toHaveBeenCalledExactlyOnceWith({ throwOnError: true }),
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Add Xtream guide" })).toBeDisabled(),
  );
});

it("does not offer an already configured provider guide", () => {
  fixture.configured = true;
  render(<XtreamGuideSourceForm />);
  expect(screen.queryByRole("option", { name: /Fixture TV/ })).toBeNull();
  expect(screen.getByRole("button", { name: "Add Xtream guide" })).toBeDisabled();
});

it("clears the selected guide provider when session or PIN authority changes", () => {
  const view = render(<XtreamGuideSourceForm />);
  fireEvent.change(screen.getByLabelText("Xtream provider"), { target: { value: "provider" } });
  fixture.pin++;
  view.rerender(<XtreamGuideSourceForm />);
  expect(screen.getByLabelText("Xtream provider")).toHaveValue("");
  expect(fixture.mutate).not.toHaveBeenCalled();
});

it("requires an explicit guide reload after a lost response, even across remounts", async () => {
  fixture.mutate.mockRejectedValueOnce(new Error("lost response"));
  const view = render(<XtreamGuideSourceForm />);
  fireEvent.change(screen.getByLabelText("Xtream provider"), { target: { value: "provider" } });
  fireEvent.click(screen.getByRole("button", { name: "Add Xtream guide" }));
  await screen.findByRole("button", { name: "Reload guide sources" });
  view.unmount();
  render(<XtreamGuideSourceForm />);
  expect(screen.getByLabelText("Xtream provider")).toBeDisabled();
  expect(fixture.refetch).not.toHaveBeenCalled();
  expect(fixture.mutate).toHaveBeenCalledTimes(1);
  fixture.refetch.mockResolvedValueOnce({ data: [] });
  fireEvent.click(screen.getByRole("button", { name: "Reload guide sources" }));
  await waitFor(() => expect(screen.getByLabelText("Xtream provider")).not.toBeDisabled());
  expect(screen.getByLabelText("Xtream provider")).toHaveValue("");
  expect(fixture.mutate).toHaveBeenCalledTimes(1);
});

it("blocks another guide write when post-create readback fails", async () => {
  fixture.refetch.mockRejectedValueOnce(new Error("readback unavailable"));
  render(<XtreamGuideSourceForm />);
  fireEvent.change(screen.getByLabelText("Xtream provider"), { target: { value: "provider" } });
  fireEvent.click(screen.getByRole("button", { name: "Add Xtream guide" }));
  await screen.findByRole("button", { name: "Reload guide sources" });
  expect(screen.getByRole("button", { name: "Add Xtream guide" })).toBeDisabled();
  expect(fixture.mutate).toHaveBeenCalledTimes(1);
});

it("does not admit a second guide creation while the original remounted write is pending", async () => {
  let resolve!: (value: { id: string }) => void;
  fixture.mutate.mockReturnValueOnce(new Promise((done) => (resolve = done)));
  const view = render(<XtreamGuideSourceForm />);
  fireEvent.change(screen.getByLabelText("Xtream provider"), { target: { value: "provider" } });
  fireEvent.click(screen.getByRole("button", { name: "Add Xtream guide" }));
  view.unmount();
  render(<XtreamGuideSourceForm />);
  expect(screen.getByRole("button", { name: "Adding guide…" })).toBeDisabled();
  expect(fixture.mutate).toHaveBeenCalledTimes(1);
  await act(async () => resolve({ id: "guide" }));
  await waitFor(() => expect(screen.getByLabelText("Xtream provider")).not.toBeDisabled());
  expect(screen.getByLabelText("Xtream provider")).toHaveValue("");
});

it("rejects stale guide intent before React has remounted the form", () => {
  render(<XtreamGuideSourceForm />);
  fireEvent.change(screen.getByLabelText("Xtream provider"), { target: { value: "provider" } });
  fixture.pin++;
  fireEvent.click(screen.getByRole("button", { name: "Add Xtream guide" }));
  expect(fixture.mutate).not.toHaveBeenCalled();
});

it("requires explicit provider-removal confirmation and explains the DVR consequences", () => {
  const remove = vi.fn();
  render(<XtreamRemoveButton id="provider" pending={false} onRemove={remove} />);
  fireEvent.click(screen.getByRole("button", { name: "Remove" }));
  expect(remove).not.toHaveBeenCalled();
  expect(screen.getByText(/associated DVR entries/)).toBeInTheDocument();
  expect(screen.getByText(/Recorded files are not deleted/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Remove provider" }));
  expect(remove).toHaveBeenCalledExactlyOnceWith("provider");
});

it("rejects a stale removal confirmation before invoking the mutation", () => {
  const remove = vi.fn();
  render(<XtreamRemoveButton id="provider" pending={false} onRemove={remove} />);
  fireEvent.click(screen.getByRole("button", { name: "Remove" }));
  fixture.generation++;
  fireEvent.click(screen.getByRole("button", { name: "Remove provider" }));
  expect(remove).not.toHaveBeenCalled();
});

it("closes removal confirmation on profile authority changes", () => {
  const remove = vi.fn();
  const view = render(<XtreamRemoveButton id="provider" pending={false} onRemove={remove} />);
  fireEvent.click(screen.getByRole("button", { name: "Remove" }));
  fixture.profile = "other";
  fixture.pin++;
  view.rerender(<XtreamRemoveButton id="provider" pending={false} onRemove={remove} />);
  expect(screen.queryByRole("button", { name: "Remove provider" })).toBeNull();
  expect(remove).not.toHaveBeenCalled();
});
