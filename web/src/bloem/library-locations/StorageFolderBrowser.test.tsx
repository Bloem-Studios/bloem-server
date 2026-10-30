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
  breadcrumbs: [],
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
      breadcrumbs: [dir("Root")],
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

it.each(["bloem.storage.s3", "bloem.storage.bookwarehouse"])(
  "keeps sibling selections while navigating nested folders in %s",
  async (plugin_id) => {
    const user = (await import("@testing-library/user-event")).default.setup();
    const root = dir("Root");
    const archive = dir("Archive");
    const seventies = dir("70s");
    const eighties = dir("80s");
    const browse = vi.fn(async (_key: string, ref = "") => {
      if (!ref || ref === root.selection_ref) return page([archive]);
      if (ref === archive.selection_ref)
        return { ...page([seventies, eighties]), current: archive, breadcrumbs: [root] };
      if (ref === seventies.selection_ref)
        return { ...page(), current: seventies, breadcrumbs: [root, archive] };
      throw new Error("Unexpected directory reference");
    });
    const select = vi.fn();
    render(
      <StorageFolderBrowser
        open
        onOpenChange={() => {}}
        api={{ browse } as unknown as LocationApi}
        source={{ ...source, plugin_id }}
        onSelect={select}
      />,
    );
    await user.click(await screen.findByRole("button", { name: "Archive" }));
    await user.click(await screen.findByRole("checkbox", { name: "Select 70s" }));
    await user.click(screen.getByRole("button", { name: "70s" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Up" })).toBeEnabled());
    expect(screen.getByLabelText("Storage folder breadcrumbs")).toHaveTextContent("70s");
    expect(screen.queryByRole("button", { name: "Use Current Folder" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Up" }));
    await user.click(await screen.findByRole("checkbox", { name: "Select 80s" }));
    expect(browse.mock.calls.at(-1)?.[1]).toBe(archive.selection_ref);
    expect(screen.getByText("2 folders selected")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Add selected folders" }));
    expect(select).toHaveBeenCalledWith([
      { ...seventies, source_key: source.source_key, configuration_revision: 1 },
      { ...eighties, source_key: source.source_key, configuration_revision: 1 },
    ]);
  },
);
const emptyRootPage = (): StoragePage => ({
  current: { name: "", logical_path: "", selection_ref: "root" },
  breadcrumbs: [],
  directories: [],
  next_cursor: "",
});
const renderEmpty = (summary: Record<string, string> | undefined, pageValue = emptyRootPage()) => {
  const browse = vi.fn().mockResolvedValue(pageValue);
  render(
    <StorageFolderBrowser
      open
      onOpenChange={() => {}}
      api={{ browse } as unknown as LocationApi}
      source={{ ...source, connection_summary: summary } as unknown as StorageSource}
      onSelect={() => {}}
    />,
  );
};
const prefixMessage =
  "This source starts at movies/10s/ and has no subfolders. Use the current folder, or edit the source's prefix.";
const plainMessage = "No subfolders. You can use the current folder.";
it("names the source prefix when the root has no subfolders", async () => {
  renderEmpty({ prefix: "movies/10s/", bucket: "media" });
  expect(await screen.findByText(prefixMessage)).toBeInTheDocument();
  expect(screen.queryByText(plainMessage)).not.toBeInTheDocument();
});
it("keeps the plain empty text for a bucket-root source", async () => {
  renderEmpty({ prefix: "" });
  expect(await screen.findByText(plainMessage)).toBeInTheDocument();
});
it("keeps the plain empty text when the source has no connection summary", async () => {
  renderEmpty(undefined);
  expect(await screen.findByText(plainMessage)).toBeInTheDocument();
});
it("keeps the plain empty text for an empty folder below the root", async () => {
  renderEmpty(
    { prefix: "movies/10s/" },
    {
      current: dir("Leaf"),
      breadcrumbs: [{ name: "", logical_path: "", selection_ref: "root" }],
      directories: [],
      next_cursor: "",
    },
  );
  expect(await screen.findByText(plainMessage)).toBeInTheDocument();
  expect(screen.queryByText(prefixMessage)).not.toBeInTheDocument();
});

const allBucketsSource = {
  ...source,
  provider_source_id: "s3_all",
  connection_summary: { source_name: "Backblaze B2", region: "us-west-004", prefix: "" },
} as unknown as StorageSource;
const bucketRoot = { name: "s3_all", logical_path: "", selection_ref: "root-ref" };
const folder = (logical_path: string) => ({
  name: logical_path.replace(/\/$/, "").split("/").pop()!,
  logical_path,
  selection_ref: logical_path + "-ref",
});
it("shows the buckets of an all-buckets source and warns before using them all", async () => {
  const browse = vi.fn().mockResolvedValue({
    current: bucketRoot,
    breadcrumbs: [],
    directories: [folder("movies/"), folder("shows/")],
    next_cursor: "",
  });
  const select = vi.fn();
  render(
    <StorageFolderBrowser
      open
      onOpenChange={() => {}}
      api={{ browse } as unknown as LocationApi}
      source={allBucketsSource}
      onSelect={select}
    />,
  );
  expect(await screen.findByText("Browse Backblaze B2 · all buckets")).toBeInTheDocument();
  expect(
    screen.getByText(
      "Each bucket is a folder. Tick whole buckets or folders inside them, in as many buckets as you like.",
    ),
  ).toBeInTheDocument();
  expect(screen.getByText("All buckets", { selector: "[aria-current=page]" })).toBeInTheDocument();
  expect(
    screen.getByText(
      "Every bucket these keys can see, including buckets added later, becomes part of this library.",
    ),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Use all buckets" }));
  expect(select).toHaveBeenCalledWith([expect.objectContaining({ selection_ref: "root-ref" })]);
});
it("counts folders picked across buckets and adds them together", async () => {
  const browse = vi.fn(async (_key: string, ref: string) =>
    ref === "shows/-ref"
      ? {
          current: folder("shows/"),
          breadcrumbs: [bucketRoot],
          directories: [folder("shows/Drama/")],
          next_cursor: "",
        }
      : {
          current: bucketRoot,
          breadcrumbs: [],
          directories: [folder("movies/"), folder("shows/")],
          next_cursor: "",
        },
  );
  const select = vi.fn();
  render(
    <StorageFolderBrowser
      open
      onOpenChange={() => {}}
      api={{ browse } as unknown as LocationApi}
      source={allBucketsSource}
      onSelect={select}
    />,
  );
  fireEvent.click(await screen.findByRole("checkbox", { name: "Select movies" }));
  fireEvent.click(screen.getByRole("button", { name: "shows" }));
  fireEvent.click(await screen.findByRole("checkbox", { name: "Select Drama" }));
  expect(screen.getByRole("button", { name: "All buckets" })).toBeInTheDocument();
  expect(screen.getByText("2 folders in 2 buckets")).toBeInTheDocument();
  expect(screen.getByText("movies/")).toBeInTheDocument();
  expect(screen.getByText("shows/Drama/")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Add selected folders" }));
  expect(select).toHaveBeenCalledWith([
    expect.objectContaining({ logical_path: "movies/" }),
    expect.objectContaining({ logical_path: "shows/Drama/" }),
  ]);
});
