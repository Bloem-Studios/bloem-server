import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { StorageArtifact, StorageSource } from "@/hooks/queries/admin/storageSources";

import { StorageSourceRow } from "./StorageSourceRow";

// Radix Select scrolls the selected option into view; jsdom has no layout.
window.HTMLElement.prototype.scrollIntoView ??= () => {};

const testSource = vi.fn();
const replaceConfig = vi.fn();
const rotate = vi.fn();
const remove = vi.fn();
const disable = vi.fn();

vi.mock("@/hooks/queries/admin/storageSources", () => ({
  useUpgradeStorageSource: () => ({ mutate: vi.fn(), isPending: false }),
  useConfigureStorageSource: () => ({ mutate: vi.fn(), isPending: false }),
  useReplaceStorageConfiguration: () => ({ mutate: replaceConfig, isPending: false }),
  useRotateStorageCredentials: () => ({ mutate: rotate, isPending: false }),
  useDisableStorageSource: () => ({ mutate: disable, isPending: false }),
  useRemoveStorageSource: () => ({ mutate: remove, isPending: false }),
  useTestStorageSource: () => ({ mutate: testSource, isPending: false }),
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
const s3Artifact: StorageArtifact = {
  artifact_key: "s3-0.3.2-linux-amd64-x",
  plugin_id: "bloem.storage.s3",
  version: "0.3.2",
  os: "linux",
  arch: "amd64",
  catalog: true,
  config_schema: [s3Schema as never],
};

const s3Source = {
  source_key: "10000000-0000-0000-0000-000000000001",
  owner_kind: "platform",
  installation_id: 33,
  plugin_id: "bloem.storage.s3",
  provider_source_id: "s3_abc",
  root_entry_id: "root",
  configuration_revision: 5,
  enabled: true,
  state: "attached",
  configured: true,
  connection_summary: {
    source_name: "Movies",
    bucket: "media",
    prefix: "movies/",
    region: "us-east-1",
    endpoint: "https://s3.example.com",
  },
} as StorageSource;

describe("StorageSourceRow", () => {
  beforeEach(() => {
    testSource.mockReset();
    replaceConfig.mockReset();
    rotate.mockReset();
    remove.mockReset();
    disable.mockReset();
  });

  it("shows the non-secret summary and nothing secret", () => {
    render(
      <ul>
        <StorageSourceRow source={s3Source} artifact={s3Artifact} />
      </ul>,
    );
    expect(screen.getByText(/media/)).toBeInTheDocument();
    expect(screen.getByText(/movies\//)).toBeInTheDocument();
    expect(screen.getByText(/s3\.example\.com/)).toBeInTheDocument();
    expect(document.body.textContent).not.toMatch(/access_key|secret_key|AKIA/);
  });

  it("tests the connection by browsing the root", () => {
    testSource.mockImplementation(
      (_s: unknown, o: { onSuccess?: (r: { ok: boolean; message: string }) => void }) =>
        o.onSuccess?.({ ok: true, message: "Connection works" }),
    );
    render(
      <ul>
        <StorageSourceRow source={s3Source} artifact={s3Artifact} />
      </ul>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Test" }));
    expect(testSource).toHaveBeenCalledWith({ source: s3Source }, expect.any(Object));
    expect(screen.getByText("Connection works")).toBeInTheDocument();
  });

  it("prefills the edit form from the summary and requires the keys again", () => {
    render(
      <ul>
        <StorageSourceRow source={s3Source} artifact={s3Artifact} />
      </ul>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.getByLabelText("Bucket")).toHaveValue("media");
    expect(screen.getByLabelText("Prefix")).toHaveValue("movies/");
    expect(screen.getByLabelText("Access key")).toHaveValue("");
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(screen.getByRole("alert")).toHaveTextContent(/Enter the keys again to save/);
    expect(replaceConfig).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "AK" } });
    fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "SK" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    const call = replaceConfig.mock.calls[0]![0] as {
      source: StorageSource;
      config: { connection: Record<string, unknown> };
    };
    expect(call.source).toBe(s3Source);
    expect(call.config.connection).toMatchObject({
      source_id: "s3_abc",
      bucket: "media",
      prefix: "movies/",
      access_key: "AK",
      secret_key: "SK",
    });
  });

  it("keeps what the user types while the form is open", () => {
    render(
      <ul>
        <StorageSourceRow source={s3Source} artifact={s3Artifact} />
      </ul>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByLabelText("Bucket"), { target: { value: "other" } });
    expect(screen.getByLabelText("Bucket")).toHaveValue("other");
  });

  it("clears the entered keys once the edit is saved", () => {
    replaceConfig.mockImplementation((_i: unknown, o: { onSuccess?: () => void }) =>
      o.onSuccess?.(),
    );
    render(
      <ul>
        <StorageSourceRow source={s3Source} artifact={s3Artifact} />
      </ul>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "AK" } });
    fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "SK" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(replaceConfig).toHaveBeenCalledTimes(1);
    expect(screen.queryByLabelText("Access key")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.getByLabelText("Access key")).toHaveValue("");
    expect(screen.getByLabelText("Secret key")).toHaveValue("");
  });

  it("switches to rotate-only when the server says the source is in use", () => {
    replaceConfig.mockImplementation(
      (_i: unknown, o: { onError?: (e: { code: string }) => void }) =>
        o.onError?.({ code: "configuration_namespace_unverified" }),
    );
    render(
      <ul>
        <StorageSourceRow source={s3Source} artifact={s3Artifact} />
      </ul>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "AK" } });
    fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "SK" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(
      screen.getByText(/used by a library; only the keys can be changed/i),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("Bucket")).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "NEWAK" } });
    fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "NEWSK" } });
    fireEvent.click(screen.getByRole("button", { name: "Rotate keys" }));
    expect(rotate).toHaveBeenCalledWith(
      { source: s3Source, accessKey: "NEWAK", secretKey: "NEWSK", sessionToken: "" },
      expect.any(Object),
    );
  });

  it("starts rotate-only when the source is known to be in use", () => {
    render(
      <ul>
        <StorageSourceRow source={s3Source} artifact={s3Artifact} inUse />
      </ul>,
    );
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Rotate keys" }));
    expect(
      screen.getByText(/used by a library; only the keys can be changed/i),
    ).toBeInTheDocument();
  });

  it("clears the rotated keys after success and leaves them after a failure", () => {
    render(
      <ul>
        <StorageSourceRow source={s3Source} artifact={s3Artifact} />
      </ul>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Rotate keys" }));
    fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "AK2" } });
    fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "SK2" } });
    expect(screen.getByLabelText("Access key")).toHaveAttribute("type", "password");
    expect(screen.getByLabelText("Secret key")).toHaveAttribute("type", "password");
    expect(screen.getByLabelText(/Session token/)).toHaveAttribute("type", "password");
    fireEvent.click(screen.getByRole("button", { name: "Rotate keys" }));
    expect(screen.getByLabelText("Access key")).toHaveValue("AK2");

    rotate.mockImplementation((_i: unknown, o: { onSuccess?: () => void }) => o.onSuccess?.());
    fireEvent.click(screen.getByRole("button", { name: "Rotate keys" }));
    expect(screen.queryByLabelText("Access key")).not.toBeInTheDocument();
  });

  it("confirms before removing and says the source is retained", () => {
    render(
      <ul>
        <StorageSourceRow source={s3Source} artifact={s3Artifact} />
      </ul>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(screen.getByText(/retained/i)).toBeInTheDocument();
    expect(remove).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: /confirm remove/i }));
    expect(remove).toHaveBeenCalledWith({ source: s3Source }, expect.any(Object));
  });

  it("disables the source through the disable call", () => {
    render(
      <ul>
        <StorageSourceRow source={s3Source} artifact={s3Artifact} />
      </ul>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Disable" }));
    expect(disable).toHaveBeenCalledWith({ source: s3Source }, expect.any(Object));
  });
});
