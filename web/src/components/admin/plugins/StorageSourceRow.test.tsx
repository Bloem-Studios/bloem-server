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
const deleteRemoved = vi.fn();
const disable = vi.fn();
const upgrade = vi.fn();
let scopeValue: "platform" | "organization" = "platform";

vi.mock("@/hooks/queries/admin/storageSources", () => ({
  useStorageScope: () => ({ active: {}, scope: scopeValue, key: scopeValue }),
  useUpgradeStorageSource: () => ({ mutate: upgrade, isPending: false }),
  useConfigureStorageSource: () => ({ mutate: vi.fn(), isPending: false }),
  useReplaceStorageConfiguration: () => ({ mutate: replaceConfig, isPending: false }),
  useRotateStorageCredentials: () => ({ mutate: rotate, isPending: false }),
  useDisableStorageSource: () => ({ mutate: disable, isPending: false }),
  useRemoveStorageSource: () => ({ mutate: remove, isPending: false }),
  useDeleteRemovedStorageSource: () => ({ mutate: deleteRemoved, isPending: false }),
  useTestStorageSource: () => ({ mutate: testSource, isPending: false }),
  useListS3Buckets: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useListS3Folders: () => ({ mutateAsync: vi.fn(), isPending: false }),
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
      bucket: { type: "string", minLength: 3, maxLength: 63 },
      prefix: { type: "string" },
      region: { type: "string" },
      endpoint: { type: "string" },
      access_key: { type: "string" },
      secret_key: { type: "string" },
      state_path: { type: "string" },
      probe_key: { type: "string" },
    },
  }),
  admin_form: {
    fields: [
      field("source_id", "Source ID"),
      field("source_name", "Name (optional)"),
      field("bucket", "Bucket"),
      field("prefix", "Prefix"),
      field("region", "Region"),
      field("endpoint", "Address"),
      field("access_key", "Access key", "PASSWORD", true),
      field("secret_key", "Secret key", "PASSWORD", true),
      field("state_path", "State path"),
      field("probe_key", "Probe key"),
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

/** The 0.4.0 release, whose schema lets the bucket be empty. */
const s3Artifact040: StorageArtifact = {
  ...s3Artifact,
  artifact_key: "s3-0.4.0-linux-amd64-y",
  version: "0.4.0",
  config_schema: [
    { ...s3Schema, json_schema: s3Schema.json_schema.replace('"minLength":3,', "") } as never,
  ],
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
  in_use: false,
  libraries: [],
} as StorageSource;

describe("StorageSourceRow", () => {
  beforeEach(() => {
    testSource.mockReset();
    replaceConfig.mockReset();
    rotate.mockReset();
    remove.mockReset();
    deleteRemoved.mockReset();
    disable.mockReset();
    upgrade.mockReset();
    scopeValue = "platform";
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
    expect(screen.getByLabelText("Folder (optional)")).toHaveValue("movies/");
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

  describe("a source covering every bucket", () => {
    const allBuckets = {
      ...s3Source,
      connection_summary: {
        source_name: "Backblaze B2",
        prefix: "",
        region: "us-west-004",
        endpoint: "https://s3.us-west-004.backblazeb2.com",
      },
    } as StorageSource;
    const edit = () => {
      render(
        <ul>
          <StorageSourceRow source={allBuckets} artifact={s3Artifact040} />
        </ul>,
      );
      fireEvent.click(screen.getByRole("button", { name: "Edit" }));
      fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "AK" } });
      fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "SK" } });
    };

    it("says so in its summary", () => {
      render(
        <ul>
          <StorageSourceRow source={allBuckets} artifact={s3Artifact040} />
        </ul>,
      );
      expect(screen.getByText("All buckets")).toBeInTheDocument();
    });

    it("saves without a bucket", () => {
      edit();
      expect(screen.getByLabelText("Bucket (optional)")).toHaveValue("");
      fireEvent.click(screen.getByRole("button", { name: /save/i }));
      const call = replaceConfig.mock.calls[0]![0] as {
        config: { connection: Record<string, unknown> };
      };
      expect(call.config.connection).not.toHaveProperty("bucket");
      expect(call.config.connection).toMatchObject({ source_name: "Backblaze B2" });
    });

    it("refuses a prefix without a bucket", () => {
      edit();
      fireEvent.change(screen.getByLabelText("Folder (optional)"), {
        target: { value: "movies/" },
      });
      fireEvent.click(screen.getByRole("button", { name: /save/i }));
      expect(screen.getByRole("alert")).toHaveTextContent(
        "A folder needs a bucket. Leave it empty to use every bucket.",
      );
      expect(replaceConfig).not.toHaveBeenCalled();
    });
  });

  it("says before saving that the keys must be entered again", () => {
    render(
      <ul>
        <StorageSourceRow source={s3Source} artifact={s3Artifact} />
      </ul>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    expect(
      screen.getByText(
        "Enter the access key and secret key again to save. Bloem keeps them encrypted and can't show them.",
      ),
    ).toBeInTheDocument();
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
      screen.getByText(/uses this source, so only its keys can be changed/i),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("Bucket")).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "NEWAK" } });
    fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "NEWSK" } });
    fireEvent.click(screen.getByRole("button", { name: "Save new keys" }));
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
    fireEvent.click(screen.getByRole("button", { name: "Replace keys" }));
    expect(
      screen.getByText(/uses this source, so only its keys can be changed/i),
    ).toBeInTheDocument();
  });

  it("clears the rotated keys after success and leaves them after a failure", () => {
    render(
      <ul>
        <StorageSourceRow source={s3Source} artifact={s3Artifact} />
      </ul>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Replace keys" }));
    fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "AK2" } });
    fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "SK2" } });
    expect(screen.getByLabelText("Access key")).toHaveAttribute("type", "password");
    expect(screen.getByLabelText("Secret key")).toHaveAttribute("type", "password");
    expect(screen.getByLabelText(/Session token/)).toHaveAttribute("type", "password");
    fireEvent.click(screen.getByRole("button", { name: "Save new keys" }));
    expect(screen.getByLabelText("Access key")).toHaveValue("AK2");

    rotate.mockImplementation((_i: unknown, o: { onSuccess?: () => void }) => o.onSuccess?.());
    fireEvent.click(screen.getByRole("button", { name: "Save new keys" }));
    expect(screen.queryByLabelText("Access key")).not.toBeInTheDocument();
  });

  describe("failed saves", () => {
    function openEditAndFillKeys() {
      render(
        <ul>
          <StorageSourceRow source={s3Source} artifact={s3Artifact} />
        </ul>,
      );
      fireEvent.click(screen.getByRole("button", { name: "Edit" }));
      fireEvent.change(screen.getByLabelText("Bucket"), { target: { value: "other-bucket" } });
      fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "AK" } });
      fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "SK" } });
      fireEvent.click(screen.getByRole("button", { name: /save/i }));
    }

    it("keeps the edit form and its values after a revision conflict", () => {
      replaceConfig.mockImplementation(
        (_i: unknown, o: { onError?: (e: { code: string }) => void }) =>
          o.onError?.({ code: "revision_conflict" }),
      );
      openEditAndFillKeys();
      expect(replaceConfig).toHaveBeenCalledTimes(1);
      expect(screen.queryByText(/uses this source/i)).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Cancel" })).toBeInTheDocument();
      expect(screen.getByLabelText("Bucket")).toHaveValue("other-bucket");
      expect(screen.getByLabelText("Access key")).toHaveValue("AK");
      expect(screen.getByLabelText("Secret key")).toHaveValue("SK");
      expect(screen.queryByLabelText(/Session token/)).not.toBeInTheDocument();
    });

    it("keeps the typed keys after any other failed replace", () => {
      replaceConfig.mockImplementation((_i: unknown, o: { onError?: (e: Error) => void }) =>
        o.onError?.(new Error("storage backend unavailable")),
      );
      openEditAndFillKeys();
      expect(replaceConfig).toHaveBeenCalledTimes(1);
      expect(screen.queryByText(/uses this source/i)).not.toBeInTheDocument();
      expect(screen.getByLabelText("Bucket")).toHaveValue("other-bucket");
      expect(screen.getByLabelText("Access key")).toHaveValue("AK");
      expect(screen.getByLabelText("Secret key")).toHaveValue("SK");
    });

    it("keeps the typed keys when rotating fails and clears them when it succeeds", () => {
      render(
        <ul>
          <StorageSourceRow source={s3Source} artifact={s3Artifact} />
        </ul>,
      );
      fireEvent.click(screen.getByRole("button", { name: "Replace keys" }));
      fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "AK2" } });
      fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "SK2" } });
      fireEvent.change(screen.getByLabelText(/Session token/), { target: { value: "TOK" } });

      rotate.mockImplementation((_i: unknown, o: { onError?: (e: Error) => void }) =>
        o.onError?.(new Error("rotation refused")),
      );
      fireEvent.click(screen.getByRole("button", { name: "Save new keys" }));
      expect(rotate).toHaveBeenCalledTimes(1);
      expect(screen.getByLabelText("Access key")).toHaveValue("AK2");
      expect(screen.getByLabelText("Secret key")).toHaveValue("SK2");
      expect(screen.getByLabelText(/Session token/)).toHaveValue("TOK");

      rotate.mockImplementation((_i: unknown, o: { onSuccess?: () => void }) => o.onSuccess?.());
      fireEvent.click(screen.getByRole("button", { name: "Save new keys" }));
      expect(rotate).toHaveBeenCalledTimes(2);
      // The form closes on success; reopening it shows empty inputs.
      expect(screen.queryByLabelText("Access key")).not.toBeInTheDocument();
      fireEvent.click(screen.getByRole("button", { name: "Replace keys" }));
      expect(screen.getByLabelText("Access key")).toHaveValue("");
      expect(screen.getByLabelText("Secret key")).toHaveValue("");
      expect(screen.getByLabelText(/Session token/)).toHaveValue("");
    });
  });

  describe("test connection", () => {
    it("shows the failure message in the status region", () => {
      testSource.mockImplementation(
        (_s: unknown, o: { onSuccess?: (r: { ok: boolean; message: string }) => void }) =>
          o.onSuccess?.({ ok: false, message: "Access denied" }),
      );
      render(
        <ul>
          <StorageSourceRow source={s3Source} artifact={s3Artifact} />
        </ul>,
      );
      expect(() => fireEvent.click(screen.getByRole("button", { name: "Test" }))).not.toThrow();
      expect(screen.getByRole("status")).toHaveTextContent("Access denied");
      expect(screen.queryByText("Connection works")).not.toBeInTheDocument();
    });

    it("disables Test for a disabled source and says why", () => {
      const disabledSource = { ...s3Source, enabled: false, state: "disabled" } as StorageSource;
      render(
        <ul>
          <StorageSourceRow source={disabledSource} artifact={s3Artifact} />
        </ul>,
      );
      const button = screen.getByRole("button", { name: "Test" });
      expect(button).toBeDisabled();
      expect(button).toHaveAccessibleDescription(/disabled source can't be tested/i);
      fireEvent.click(button);
      expect(testSource).not.toHaveBeenCalled();
    });

    function showTestResult() {
      testSource.mockImplementation(
        (_s: unknown, o: { onSuccess?: (r: { ok: boolean; message: string }) => void }) =>
          o.onSuccess?.({ ok: true, message: "Connection works" }),
      );
      fireEvent.click(screen.getByRole("button", { name: "Test" }));
      expect(screen.getByRole("status")).toHaveTextContent("Connection works");
    }

    it("clears a stale test result after a successful edit save", () => {
      replaceConfig.mockImplementation((_i: unknown, o: { onSuccess?: () => void }) =>
        o.onSuccess?.(),
      );
      render(
        <ul>
          <StorageSourceRow source={s3Source} artifact={s3Artifact} />
        </ul>,
      );
      showTestResult();
      fireEvent.click(screen.getByRole("button", { name: "Edit" }));
      fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "AK" } });
      fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "SK" } });
      fireEvent.click(screen.getByRole("button", { name: /save/i }));
      expect(replaceConfig).toHaveBeenCalledTimes(1);
      expect(screen.queryByRole("status")).not.toBeInTheDocument();
    });

    it("keeps the test result when the edit save fails", () => {
      replaceConfig.mockImplementation((_i: unknown, o: { onError?: (e: Error) => void }) =>
        o.onError?.(new Error("nope")),
      );
      render(
        <ul>
          <StorageSourceRow source={s3Source} artifact={s3Artifact} />
        </ul>,
      );
      showTestResult();
      fireEvent.click(screen.getByRole("button", { name: "Edit" }));
      fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "AK" } });
      fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "SK" } });
      fireEvent.click(screen.getByRole("button", { name: /save/i }));
      expect(screen.getByRole("status")).toHaveTextContent("Connection works");
    });

    it("clears a stale test result after the keys are rotated", () => {
      rotate.mockImplementation((_i: unknown, o: { onSuccess?: () => void }) => o.onSuccess?.());
      render(
        <ul>
          <StorageSourceRow source={s3Source} artifact={s3Artifact} />
        </ul>,
      );
      showTestResult();
      fireEvent.click(screen.getByRole("button", { name: "Replace keys" }));
      fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "AK2" } });
      fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "SK2" } });
      fireEvent.click(screen.getByRole("button", { name: "Save new keys" }));
      expect(rotate).toHaveBeenCalledTimes(1);
      expect(screen.queryByRole("status")).not.toBeInTheDocument();
    });
  });

  describe("plain HTTP on loopback", () => {
    const loopback = {
      ...s3Source,
      connection_summary: { ...s3Source.connection_summary, allow_loopback_http: "true" },
    } as StorageSource;

    it("locks the whole-configuration edit but keeps the other actions", () => {
      render(
        <ul>
          <StorageSourceRow source={loopback} artifact={s3Artifact} />
        </ul>,
      );
      expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
      expect(
        screen.getByText(
          "This source uses a local address without https, so only its keys can be changed here.",
        ),
      ).toBeInTheDocument();
      for (const name of ["Test", "Replace keys", "Remove"]) {
        expect(screen.getByRole("button", { name })).toBeInTheDocument();
      }
    });

    it("shows no explanation and keeps Edit otherwise", () => {
      render(
        <ul>
          <StorageSourceRow source={s3Source} artifact={s3Artifact} />
        </ul>,
      );
      expect(screen.getByRole("button", { name: "Edit" })).toBeInTheDocument();
      expect(screen.queryByText(/local address without https/i)).not.toBeInTheDocument();
    });
  });

  describe("which actions are offered", () => {
    function renderRow(source: StorageSource) {
      render(
        <ul>
          <StorageSourceRow source={source} artifact={s3Artifact} />
        </ul>,
      );
    }
    const queryAction = (name: string) => screen.queryByRole("button", { name });

    it("hides Edit and Replace keys for a source that is not manageable", () => {
      renderRow({ ...s3Source, installation_id: null } as StorageSource);
      expect(queryAction("Edit")).not.toBeInTheDocument();
      expect(queryAction("Replace keys")).not.toBeInTheDocument();
    });

    it("hides Edit and Replace keys for a disabled source", () => {
      renderRow({ ...s3Source, enabled: false, state: "disabled" } as StorageSource);
      expect(queryAction("Edit")).not.toBeInTheDocument();
      expect(queryAction("Replace keys")).not.toBeInTheDocument();
    });

    it("hides every management action on a platform source in an organization scope", () => {
      scopeValue = "organization";
      renderRow(s3Source);
      for (const name of ["Edit", "Replace keys", "Disable", "Remove"]) {
        expect(queryAction(name)).not.toBeInTheDocument();
      }
    });

    it("keeps the management actions on an organization source in an organization scope", () => {
      scopeValue = "organization";
      renderRow({ ...s3Source, owner_kind: "organization" } as StorageSource);
      for (const name of ["Test", "Edit", "Replace keys", "Remove"]) {
        expect(queryAction(name)).toBeEnabled();
      }
    });

    it("lets an organization admin use those actions on their own S3 source", () => {
      scopeValue = "organization";
      const own = { ...s3Source, owner_kind: "organization" } as StorageSource;
      renderRow(own);
      fireEvent.click(screen.getByRole("button", { name: "Test" }));
      expect(testSource).toHaveBeenCalledWith({ source: own }, expect.any(Object));
      fireEvent.click(screen.getByRole("button", { name: "Edit" }));
      fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "AK" } });
      fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "SK" } });
      fireEvent.click(screen.getByRole("button", { name: /save/i }));
      expect(replaceConfig).toHaveBeenCalledTimes(1);
      fireEvent.click(screen.getByRole("button", { name: "Remove" }));
      fireEvent.click(screen.getByRole("button", { name: /remove source/i }));
      expect(remove).toHaveBeenCalledWith({ source: own }, expect.any(Object));
    });

    it("hides Upgrade for an S3 source in an organization scope but not in the platform scope", () => {
      scopeValue = "organization";
      const own = { ...s3Source, owner_kind: "organization" } as StorageSource;
      const { unmount } = render(
        <ul>
          <StorageSourceRow source={own} artifact={s3Artifact} artifacts={[s3Artifact]} />
        </ul>,
      );
      expect(queryAction("Upgrade")).not.toBeInTheDocument();
      unmount();
      scopeValue = "platform";
      renderRow(s3Source);
      expect(queryAction("Upgrade")).toBeInTheDocument();
    });
  });

  describe("probe key", () => {
    const withProbe = {
      ...s3Source,
      connection_summary: { ...s3Source.connection_summary, probe_key: "movies/x.mkv" },
    } as StorageSource;

    it("is not asked for when editing, even when the summary carries one", () => {
      render(
        <ul>
          <StorageSourceRow source={withProbe} artifact={s3Artifact} />
        </ul>,
      );
      fireEvent.click(screen.getByRole("button", { name: "Edit" }));
      expect(screen.getByLabelText("Bucket")).toHaveValue("media");
      expect(screen.queryByLabelText("Probe key")).not.toBeInTheDocument();
    });

    it("is not sent when the whole configuration is replaced", () => {
      render(
        <ul>
          <StorageSourceRow source={withProbe} artifact={s3Artifact} />
        </ul>,
      );
      fireEvent.click(screen.getByRole("button", { name: "Edit" }));
      fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "AK" } });
      fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "SK" } });
      fireEvent.click(screen.getByRole("button", { name: /save/i }));
      const call = replaceConfig.mock.calls[0]![0] as {
        config: { connection: Record<string, unknown> };
      };
      expect(call.config.connection).not.toHaveProperty("probe_key");
      expect(JSON.stringify(call.config)).not.toContain("movies/x.mkv");
    });

    it("leaves the server's summary untouched", () => {
      render(
        <ul>
          <StorageSourceRow source={withProbe} artifact={s3Artifact} />
        </ul>,
      );
      expect(withProbe.connection_summary.probe_key).toBe("movies/x.mkv");
    });
  });

  describe("upgrade", () => {
    it("offers only catalog releases of the source's own plugin", () => {
      const staticS3 = {
        ...s3Artifact,
        artifact_key: "s3-static-0.3.1",
        version: "0.3.1",
        catalog: false,
      } as StorageArtifact;
      const other = {
        ...s3Artifact,
        artifact_key: "bw-0.9.0",
        plugin_id: "bloem.storage.bookwarehouse",
        version: "0.9.0",
      } as StorageArtifact;
      render(
        <ul>
          <StorageSourceRow
            source={s3Source}
            artifact={s3Artifact}
            artifacts={[staticS3, s3Artifact, other]}
          />
        </ul>,
      );
      fireEvent.click(screen.getByRole("button", { name: "Upgrade" }));
      fireEvent.click(screen.getByRole("combobox", { name: "Version to upgrade to" }));
      const options = screen.getAllByRole("option");
      expect(options).toHaveLength(1);
      expect(options[0]).toHaveTextContent("Version 0.3.2");
    });
  });

  it("confirms before removing and says nothing in the storage is deleted", () => {
    render(
      <ul>
        <StorageSourceRow source={s3Source} artifact={s3Artifact} />
      </ul>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(screen.getByText(/Nothing in your storage is deleted/i)).toBeInTheDocument();
    expect(remove).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: /remove source/i }));
    expect(remove).toHaveBeenCalledWith({ source: s3Source }, expect.any(Object));
  });

  it("offers no Disable, which would detach the source just like Remove", () => {
    render(
      <ul>
        <StorageSourceRow source={s3Source} artifact={s3Artifact} />
      </ul>,
    );
    expect(screen.queryByRole("button", { name: "Disable" })).not.toBeInTheDocument();
    expect(screen.getByText("Live")).toBeInTheDocument();
    expect(screen.getByText("Not used by any library.")).toBeInTheDocument();
  });

  it("warns that Remove can't be undone before removing", () => {
    render(
      <ul>
        <StorageSourceRow source={s3Source} artifact={s3Artifact} />
      </ul>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(screen.getByText(/This can't be undone/)).toBeInTheDocument();
    expect(remove).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Keep" }));
    expect(screen.queryByRole("button", { name: "Remove source" })).not.toBeInTheDocument();
    expect(remove).not.toHaveBeenCalled();
  });

  it("names the libraries using a source and offers no Remove", () => {
    const used = {
      ...s3Source,
      in_use: true,
      libraries: [
        { id: 25, name: "Electronic Books" },
        { id: 28, name: "Audio books" },
      ],
    } as StorageSource;
    render(
      <ul>
        <StorageSourceRow source={used} artifact={s3Artifact} />
      </ul>,
    );
    expect(screen.getByText(/Used by Electronic Books, Audio books\./)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Replace keys" })).toBeInTheDocument();
  });

  it("says another organization's library uses a source without naming it", () => {
    render(
      <ul>
        <StorageSourceRow source={{ ...s3Source, in_use: true, libraries: [] } as StorageSource} />
      </ul>,
    );
    expect(screen.getByText("Used by a library in another organization.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();
  });

  it("marks a source whose plugin is gone as removed, with no actions", () => {
    const gone = {
      ...s3Source,
      installation_id: null,
      enabled: false,
      state: "detached",
      configured: false,
      connection_summary: {},
    } as StorageSource;
    render(
      <ul>
        <StorageSourceRow source={gone} />
      </ul>,
    );
    expect(screen.getByText("Removed")).toBeInTheDocument();
    expect(screen.getByText(/can't be turned back on/)).toBeInTheDocument();
    expect(screen.getAllByRole("button").map((b) => b.textContent)).toEqual(["Delete"]);
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(deleteRemoved).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Delete source" }));
    expect(deleteRemoved).toHaveBeenCalledWith({ source: gone }, expect.any(Object));
  });

  describe("the request limits and proxy of the 0.5.0 release", () => {
    const limitFields: [string, string][] = [
      ["auth_pause_seconds", "Pause after refused keys (seconds, optional)"],
      ["auth_pause_max_seconds", "Longest pause (seconds, optional)"],
      ["auth_failure_budget", "Refusals allowed per window (optional)"],
      ["auth_failure_window_seconds", "Refusal window (seconds, optional)"],
      ["max_concurrent_requests", "Requests at once (optional)"],
      ["max_listing_requests", "Requests at once for scans (optional)"],
      ["max_read_mbps", "Speed limit in Mbps (optional)"],
    ];
    const parsed = JSON.parse(s3Schema.json_schema) as { properties: Record<string, unknown> };
    for (const [key] of limitFields) parsed.properties[key] = { type: "integer" };
    for (const key of ["http_proxy", "http_proxy_username", "http_proxy_password"])
      parsed.properties[key] = { type: "string" };
    const s3Artifact050: StorageArtifact = {
      ...s3Artifact,
      artifact_key: "s3-0.5.0-linux-amd64-z",
      version: "0.5.0",
      config_schema: [
        {
          ...s3Schema,
          json_schema: JSON.stringify(parsed),
          admin_form: {
            fields: [
              ...s3Schema.admin_form.fields,
              ...limitFields.map(([key, label]) => field(key, label, "NUMBER")),
              field("http_proxy", "Proxy (optional)"),
              field("http_proxy_username", "Proxy user name (optional)"),
              field("http_proxy_password", "Proxy password (optional)", "PASSWORD", true),
            ],
          },
        } as never,
      ],
    };
    const tuned = {
      ...s3Source,
      connection_summary: {
        ...s3Source.connection_summary,
        max_read_mbps: "90",
        max_listing_requests: "3",
        http_proxy: "http://proxy.example:3128",
        http_proxy_username: "squid",
      },
    } as StorageSource;
    const editAndEnterKeys = (source: StorageSource) => {
      render(
        <ul>
          <StorageSourceRow source={source} artifact={s3Artifact050} />
        </ul>,
      );
      fireEvent.click(screen.getByRole("button", { name: "Edit" }));
      fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "AK" } });
      fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "SK" } });
    };
    const openAdvanced = () => fireEvent.click(screen.getByRole("button", { name: "Show" }));
    const saved = () =>
      (replaceConfig.mock.calls[0]![0] as { config: { connection: Record<string, unknown> } })
        .config.connection;

    it("groups them under a collapsed Advanced options section", () => {
      editAndEnterKeys(tuned);
      expect(screen.getByText("Advanced options")).toBeInTheDocument();
      expect(screen.queryByLabelText("Speed limit in Mbps (optional)")).not.toBeInTheDocument();
      expect(screen.getByLabelText("Bucket")).toBeInTheDocument();
      openAdvanced();
      for (const [, label] of limitFields) expect(screen.getByLabelText(label)).toBeInTheDocument();
      expect(screen.getByLabelText("Proxy password (optional)")).toHaveAttribute(
        "type",
        "password",
      );
    });

    it("keeps the stored values it was given and leaves the proxy password to the server", () => {
      editAndEnterKeys(tuned);
      openAdvanced();
      expect(screen.getByLabelText("Speed limit in Mbps (optional)")).toHaveValue(90);
      expect(screen.getByLabelText("Proxy (optional)")).toHaveValue("http://proxy.example:3128");
      expect(screen.getByLabelText("Proxy password (optional)")).toHaveValue("");
      fireEvent.click(screen.getByRole("button", { name: /save/i }));
      expect(saved()).toMatchObject({
        max_read_mbps: 90,
        max_listing_requests: 3,
        http_proxy: "http://proxy.example:3128",
        http_proxy_username: "squid",
      });
      // Left out, the stored password stays for the same proxy and user.
      expect(saved()).not.toHaveProperty("http_proxy_password");
    });

    it("sends a cleared limit or proxy as null so the server clears it", () => {
      editAndEnterKeys(tuned);
      openAdvanced();
      fireEvent.change(screen.getByLabelText("Speed limit in Mbps (optional)"), {
        target: { value: "" },
      });
      fireEvent.change(screen.getByLabelText("Proxy (optional)"), { target: { value: "" } });
      fireEvent.click(screen.getByRole("button", { name: /save/i }));
      expect(saved()).toMatchObject({ max_read_mbps: null, http_proxy: null });
      expect(saved().max_listing_requests).toBe(3);
    });

    it("sends a newly typed proxy password", () => {
      editAndEnterKeys(tuned);
      openAdvanced();
      fireEvent.change(screen.getByLabelText("Proxy password (optional)"), {
        target: { value: "pw" },
      });
      fireEvent.click(screen.getByRole("button", { name: /save/i }));
      expect(saved().http_proxy_password).toBe("pw");
    });

    it("never shows or sends the proxy fields to an organization administrator", () => {
      scopeValue = "organization";
      editAndEnterKeys({ ...tuned, owner_kind: "organization" } as StorageSource);
      openAdvanced();
      expect(screen.getByLabelText("Speed limit in Mbps (optional)")).toHaveValue(90);
      for (const label of [
        "Proxy (optional)",
        "Proxy user name (optional)",
        "Proxy password (optional)",
      ])
        expect(screen.queryByLabelText(label)).not.toBeInTheDocument();
      fireEvent.click(screen.getByRole("button", { name: /save/i }));
      for (const key of ["http_proxy", "http_proxy_username", "http_proxy_password"])
        expect(saved()).not.toHaveProperty(key);
      expect(saved().max_read_mbps).toBe(90);
    });
  });
});
