import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { StorageArtifact, StorageSource } from "@/hooks/queries/admin/storageSources";

import { AddStorageSourceDialog } from "./AddStorageSourceDialog";

window.HTMLElement.prototype.scrollIntoView ??= () => {};

const discover = vi.fn();
const install = vi.fn();
let scope: "platform" | "organization" = "platform";

vi.mock("@/hooks/queries/admin/storageSources", () => ({
  useDiscoverStorageSource: () => ({ mutate: discover, isPending: false }),
  useInstallStorageSource: () => ({ mutate: install, isPending: false }),
  useStorageScope: () => ({ active: {}, scope, key: scope }),
}));

const field = (key: string, label: string, control = "TEXT", secret = false) => ({
  key,
  label,
  control,
  required: false,
  secret,
  multiline: false,
});
const s3Schema = {
  key: "connection",
  title: "S3 storage connection",
  required: true,
  json_schema: JSON.stringify({
    type: "object",
    properties: {
      source_id: { type: "string" },
      source_name: { type: "string" },
      bucket: { type: "string" },
      prefix: { type: "string" },
      region: { type: "string" },
      endpoint: { type: "string" },
      access_key: { type: "string" },
      secret_key: { type: "string" },
      state_path: { type: "string" },
    },
  }),
  admin_form: {
    fields: [
      field("source_id", "Source ID"),
      field("source_name", "Source name"),
      field("bucket", "Bucket"),
      field("prefix", "Prefix"),
      field("region", "Region"),
      field("endpoint", "S3 endpoint"),
      field("access_key", "Access key", "PASSWORD", true),
      field("secret_key", "Secret key", "PASSWORD", true),
      field("state_path", "State path"),
    ],
  },
};
const artifacts: StorageArtifact[] = [
  {
    artifact_key: "s3-0.3.2-linux-amd64-x",
    plugin_id: "bloem.storage.s3",
    version: "0.3.2",
    os: "linux",
    arch: "amd64",
    catalog: true,
    config_schema: [s3Schema as never],
  },
  {
    artifact_key: "bookwarehouse-0.2.0",
    plugin_id: "bloem.storage.bookwarehouse",
    version: "0.2.0",
    os: "linux",
    arch: "amd64",
    catalog: true,
    config_schema: [],
  },
];
const sources: StorageSource[] = [];

function open(props: Partial<React.ComponentProps<typeof AddStorageSourceDialog>> = {}) {
  render(
    <AddStorageSourceDialog
      artifacts={artifacts}
      sources={sources}
      open
      onOpenChange={() => {}}
      {...props}
    />,
  );
}
function fill(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

describe("AddStorageSourceDialog", () => {
  beforeEach(() => {
    discover.mockReset();
    install.mockReset();
    scope = "platform";
  });

  it("offers S3 and Bookwarehouse to platform admins", () => {
    open();
    expect(screen.getByRole("button", { name: /^S3$/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Install Bookwarehouse" })).toBeInTheDocument();
  });

  it("does not offer S3 in organization scope", () => {
    scope = "organization";
    open();
    expect(screen.queryByRole("button", { name: /^S3$/ })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Install Bookwarehouse" })).toBeInTheDocument();
  });

  it("hides the source id and state path and masks the keys", () => {
    open();
    fireEvent.click(screen.getByRole("button", { name: /^S3$/ }));
    expect(screen.getByLabelText("Bucket")).toBeInTheDocument();
    expect(screen.queryByLabelText("Source ID")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("State path")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Access key")).toHaveAttribute("type", "password");
    expect(screen.getByLabelText("Secret key")).toHaveAttribute("type", "password");
  });

  it("blocks a prefix without a trailing slash before calling the API", () => {
    open();
    fireEvent.click(screen.getByRole("button", { name: /^S3$/ }));
    fill("Bucket", "media");
    fill("Prefix", "movies");
    fill("Access key", "AK");
    fill("Secret key", "SK");
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(screen.getByRole("alert")).toHaveTextContent(/end with a slash/);
    expect(discover).not.toHaveBeenCalled();
  });

  it("discovers with matching ids, no state path, and clears the keys afterwards", () => {
    discover.mockImplementation((_input: unknown, opts: { onSuccess?: () => void }) =>
      opts.onSuccess?.(),
    );
    open();
    fireEvent.click(screen.getByRole("button", { name: /^S3$/ }));
    fill("Bucket", "media");
    fill("Prefix", "movies/");
    fill("Region", "us-east-1");
    fill("S3 endpoint", "https://s3.example.com");
    fill("Access key", "AK");
    fill("Secret key", "SK");
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    const input = discover.mock.calls[0]![0] as {
      artifactKey: string;
      providerSourceId: string;
      config: { connection: Record<string, unknown> };
    };
    expect(input.artifactKey).toBe("s3-0.3.2-linux-amd64-x");
    expect(input.providerSourceId).toMatch(/^s3_[a-f0-9]{32}$/);
    expect(input.config.connection.source_id).toBe(input.providerSourceId);
    expect(input.config.connection).not.toHaveProperty("state_path");
    expect(input.config.connection).toMatchObject({
      bucket: "media",
      prefix: "movies/",
      access_key: "AK",
      secret_key: "SK",
    });
  });

  it("empties the key fields once the source is saved", () => {
    discover.mockImplementation((_input: unknown, opts: { onSuccess?: () => void }) =>
      opts.onSuccess?.(),
    );
    open();
    fireEvent.click(screen.getByRole("button", { name: /^S3$/ }));
    fill("Bucket", "media");
    fill("Access key", "AK");
    fill("Secret key", "SK");
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(discover).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("Access key")).toHaveValue("");
    expect(screen.getByLabelText("Secret key")).toHaveValue("");
  });

  describe("source name", () => {
    function saveWith(prefix: string, name?: string) {
      open();
      fireEvent.click(screen.getByRole("button", { name: /^S3$/ }));
      fill("Bucket", "media");
      if (prefix) fill("Prefix", prefix);
      if (name !== undefined) fill("Source name", name);
      fill("Access key", "AK");
      fill("Secret key", "SK");
      fireEvent.click(screen.getByRole("button", { name: /save/i }));
      const input = discover.mock.calls[0]![0] as {
        config: { connection: Record<string, unknown> };
      };
      return input.config.connection.source_name;
    }
    it("defaults a blank name to the bucket", () => {
      expect(saveWith("")).toBe("media");
    });
    it("defaults a blank name to bucket/prefix when a prefix is set", () => {
      expect(saveWith("movies/", "   ")).toBe("media/movies/");
    });
    it("uses a typed name, trimmed", () => {
      expect(saveWith("movies/", " Movies ")).toBe("Movies");
    });
  });

  it("warns about a duplicate bucket and prefix but still allows saving", () => {
    const existing = {
      ...({} as StorageSource),
      plugin_id: "bloem.storage.s3",
      connection_summary: { bucket: "media", prefix: "movies/" },
    } as StorageSource;
    open({ sources: [existing] });
    fireEvent.click(screen.getByRole("button", { name: /^S3$/ }));
    fill("Bucket", "media");
    fill("Prefix", "movies/");
    expect(
      screen.getByText(/A source with this bucket and prefix already exists/),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /save/i })).toBeEnabled();
  });

  it("shows a retryable message when the plugin catalog is unavailable", () => {
    const retry = vi.fn();
    open({ artifacts: undefined, error: new Error("boom"), onRetry: retry });
    expect(screen.getByText(/plugin catalog unavailable/i)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /retry/i }));
    expect(retry).toHaveBeenCalled();
  });
});
