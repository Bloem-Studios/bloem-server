import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { StorageFolderBrowser } from "./StorageFolderBrowser";
import type { LocationApi } from "./api";
import type { StoragePage, StorageSource } from "./types";
const source = {
  source_key: "s3",
  configuration_revision: 1,
  plugin_id: "bloem.storage.s3",
  provider_source_id: "Archive",
} as StorageSource;
const dir = (name: string) => ({ name, logical_path: name, selection_ref: name + "-ref" });
const page = (directories: StoragePage["directories"] = [], next_cursor = ""): StoragePage => ({
  current: dir("Root"),
  breadcrumbs: [dir("Root")],
  directories,
  next_cursor,
});
it("continues empty nonterminal pages and selects the current empty folder", async () => {
  const browse = vi.fn().mockResolvedValueOnce(page([], "next")).mockResolvedValueOnce(page());
  const select = vi.fn();
  render(
    <StorageFolderBrowser
      open
      onOpenChange={() => {}}
      api={{ browse } as unknown as LocationApi}
      source={source}
      onSelect={select}
    />,
  );
  fireEvent.click(await screen.findByRole("button", { name: "Load more folders" }));
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: "Load more folders" })).not.toBeInTheDocument(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Use Current Folder" }));
  expect(select).toHaveBeenCalledWith([
    expect.objectContaining({ source_key: "s3", selection_ref: "Root-ref" }),
  ]);
});
it("cancel discards selections and ignores late pages", async () => {
  let release!: (page: StoragePage) => void;
  const browse = vi
    .fn()
    .mockResolvedValueOnce(page([dir("Books")], "next"))
    .mockImplementationOnce(
      () =>
        new Promise<StoragePage>((r) => {
          release = r;
        }),
    );
  const select = vi.fn();
  const close = vi.fn();
  const props = {
    open: true,
    onOpenChange: close,
    api: { browse } as unknown as LocationApi,
    source,
    onSelect: select,
  };
  const { rerender } = render(<StorageFolderBrowser {...props} />);
  fireEvent.click(await screen.findByRole("checkbox", { name: "Select Books" }));
  fireEvent.click(screen.getByRole("button", { name: "Load more folders" }));
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  rerender(<StorageFolderBrowser {...props} open={false} />);
  await act(async () => release(page([dir("Late")])));
  expect(select).not.toHaveBeenCalled();
  rerender(
    <StorageFolderBrowser
      {...props}
      api={{ browse: vi.fn().mockResolvedValue(page()) } as unknown as LocationApi}
    />,
  );
  expect(await screen.findByRole("button", { name: "Add selected folders" })).toBeDisabled();
});
it("a delayed source response cannot replace the new source", async () => {
  let release!: (page: StoragePage) => void;
  const api = {
    browse: vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise<StoragePage>((r) => {
            release = r;
          }),
      )
      .mockResolvedValue(page([dir("New")])),
  } as unknown as LocationApi;
  const props = { open: true, onOpenChange: () => {}, api, source, onSelect: () => {} };
  const { rerender } = render(<StorageFolderBrowser {...props} />);
  rerender(<StorageFolderBrowser {...props} source={{ ...source, source_key: "other" }} />);
  await screen.findByRole("checkbox", { name: "Select New" });
  await act(async () => release(page([dir("Old")])));
  expect(screen.queryByRole("checkbox", { name: "Select Old" })).not.toBeInTheDocument();
});
it("retry preserves selected folders after a provider failure", async () => {
  const browse = vi
    .fn()
    .mockResolvedValueOnce(page([dir("Books")], "next"))
    .mockRejectedValueOnce(new Error("Unavailable"))
    .mockResolvedValueOnce(page([dir("More")]));
  const select = vi.fn();
  render(
    <StorageFolderBrowser
      open
      onOpenChange={() => {}}
      api={{ browse } as unknown as LocationApi}
      source={source}
      onSelect={select}
    />,
  );
  fireEvent.click(await screen.findByRole("checkbox", { name: "Select Books" }));
  fireEvent.click(screen.getByRole("button", { name: "Load more folders" }));
  await screen.findByText("Unavailable");
  fireEvent.click(screen.getByRole("button", { name: "Retry" }));
  await screen.findByRole("checkbox", { name: "Select More" });
  fireEvent.click(screen.getByRole("button", { name: "Add selected folders" }));
  expect(select).toHaveBeenCalledWith([expect.objectContaining({ name: "Books" })]);
});
it("supports breadcrumbs, Up, and keyboard Escape cancellation", async () => {
  const user = (await import("@testing-library/user-event")).default.setup();
  const browse = vi
    .fn()
    .mockResolvedValueOnce(page([dir("Books")]))
    .mockResolvedValueOnce({
      ...page(),
      current: dir("Books"),
      breadcrumbs: [dir("Root"), dir("Books")],
    })
    .mockResolvedValueOnce(page());
  const close = vi.fn();
  render(
    <StorageFolderBrowser
      open
      onOpenChange={close}
      api={{ browse } as unknown as LocationApi}
      source={source}
      onSelect={() => {}}
    />,
  );
  await user.click(await screen.findByRole("button", { name: "Books" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Up" })).toBeEnabled());
  await user.click(screen.getByRole("button", { name: "Up" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Up" })).toBeDisabled());
  expect(browse.mock.calls[1]![1]).toBe("Books-ref");
  expect(browse.mock.calls[2]![1]).toBe("Root-ref");
  await user.keyboard("{Escape}");
  expect(close).toHaveBeenCalledWith(false);
});
