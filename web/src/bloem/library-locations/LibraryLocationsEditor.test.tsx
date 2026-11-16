import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { LibraryFormController } from "@/components/admin/libraries/useLibraryForm";
import { LibraryLocationsEditor } from "./LibraryLocationsEditor";
import type { StorageSource } from "./types";

vi.mock("@/components/PathAutocompleteInput", () => ({
  default: ({
    onValueChange,
    ...props
  }: React.ComponentProps<"input"> & { onValueChange: (value: string) => void }) => (
    <input {...props} onChange={(e) => onValueChange(e.target.value)} />
  ),
}));
vi.mock("@/components/FolderBrowser", () => ({ default: () => null }));

const source = {
  source_key: "s3",
  configuration_revision: 1,
  plugin_id: "bloem.storage.s3",
  provider_source_id: "Archive",
  enabled: true,
  configured: true,
} as StorageSource;

function form(pathError?: string) {
  return {
    type: "movies",
    paths: ["/media/movies", "/media/more"],
    errors: { paths: pathError },
    updatePath: vi.fn(),
    removePath: vi.fn(),
    addPath: vi.fn(),
    mergeBrowsedPaths: vi.fn(),
    locations: {
      original: null,
      selections: [
        { source_key: "s3", selection_ref: "ref", logical_path: "Films", name: "Films" },
      ],
      caps: {
        library_location_mixing: true,
        supported_source_media: [{ source_kind: "s3", media_kinds: ["movies"] }],
      },
      sources: [source],
      loading: false,
      error: "",
      uncertainCreate: false,
      conflict: false,
      ready: true,
      pending: false,
      api: null,
      shapeError: () => "",
      reload: vi.fn(),
      removeSelection: vi.fn(),
      addSelections: vi.fn(),
      isCurrent: () => true,
    },
  } as unknown as LibraryFormController;
}

const classes = (el: Element) => el.className.split(/\s+/);

it("marks the folder path fields invalid when the paths have an error, as Silo does", () => {
  const { rerender } = render(<LibraryLocationsEditor form={form()} />);
  for (const input of screen.getAllByRole("textbox")) {
    expect(input).not.toHaveAttribute("aria-invalid");
  }
  rerender(<LibraryLocationsEditor form={form("Add at least one folder")} />);
  const inputs = screen.getAllByRole("textbox");
  expect(inputs).toHaveLength(2);
  for (const input of inputs) expect(input).toHaveAttribute("aria-invalid", "true");
});

// The class tokens Silo's FolderFields gives its controls for 44 px phone
// touch targets (web/src/components/admin/libraries/LibraryFormSections.tsx).
it("gives the folder controls Silo's phone touch targets", () => {
  render(<LibraryLocationsEditor form={form()} />);
  for (const input of screen.getAllByRole("textbox")) {
    expect(classes(input)).toEqual(expect.arrayContaining(["h-11", "sm:h-9"]));
  }
  const removes = ["Remove filesystem folder 1", "Remove filesystem folder 2"].map((name) =>
    screen.getByRole("button", { name }),
  );
  removes.forEach((button, i) => {
    expect(classes(button)).toEqual(expect.arrayContaining(["size-11", "shrink-0", "sm:size-9"]));
    expect(button).toHaveAttribute("aria-describedby", screen.getAllByRole("textbox").at(i)?.id);
  });
  for (const name of ["Browse folders", "Add Path"]) {
    expect(classes(screen.getByRole("button", { name }))).toEqual(
      expect.arrayContaining(["h-11", "flex-1", "sm:h-8", "sm:flex-none"]),
    );
  }
  expect(classes(screen.getByRole("button", { name: "Remove Films" }))).toEqual(
    expect.arrayContaining(["size-11", "shrink-0", "sm:size-9"]),
  );
  expect(classes(screen.getByRole("combobox", { name: "Choose a storage source" }))).toEqual(
    expect.arrayContaining(["data-[size=default]:h-11", "sm:data-[size=default]:h-9"]),
  );
});
