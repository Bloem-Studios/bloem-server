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
      bucket: { type: "string" },
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
      field("source_name", "Source name"),
      field("bucket", "Bucket"),
      field("prefix", "Prefix"),
      field("region", "Region"),
      field("endpoint", "S3 endpoint"),
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
      expect(screen.queryByText(/used by a library/i)).not.toBeInTheDocument();
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
      expect(screen.queryByText(/used by a library/i)).not.toBeInTheDocument();
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
      fireEvent.click(screen.getByRole("button", { name: "Rotate keys" }));
      fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "AK2" } });
      fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "SK2" } });
      fireEvent.change(screen.getByLabelText(/Session token/), { target: { value: "TOK" } });

      rotate.mockImplementation((_i: unknown, o: { onError?: (e: Error) => void }) =>
        o.onError?.(new Error("rotation refused")),
      );
      fireEvent.click(screen.getByRole("button", { name: "Rotate keys" }));
      expect(rotate).toHaveBeenCalledTimes(1);
      expect(screen.getByLabelText("Access key")).toHaveValue("AK2");
      expect(screen.getByLabelText("Secret key")).toHaveValue("SK2");
      expect(screen.getByLabelText(/Session token/)).toHaveValue("TOK");

      rotate.mockImplementation((_i: unknown, o: { onSuccess?: () => void }) => o.onSuccess?.());
      fireEvent.click(screen.getByRole("button", { name: "Rotate keys" }));
      expect(rotate).toHaveBeenCalledTimes(2);
      // The form closes on success; reopening it shows empty inputs.
      expect(screen.queryByLabelText("Access key")).not.toBeInTheDocument();
      fireEvent.click(screen.getByRole("button", { name: "Rotate keys" }));
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
      expect(button).toHaveAccessibleDescription(/enable the source/i);
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
      fireEvent.click(screen.getByRole("button", { name: "Rotate keys" }));
      fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "AK2" } });
      fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "SK2" } });
      fireEvent.click(screen.getByRole("button", { name: "Rotate keys" }));
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
          "This source uses plain HTTP on loopback; only its keys can be changed here.",
        ),
      ).toBeInTheDocument();
      for (const name of ["Test", "Rotate keys", "Disable", "Remove"]) {
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
      expect(screen.queryByText(/plain HTTP on loopback/i)).not.toBeInTheDocument();
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

    it("hides Edit and Rotate keys for a source that is not manageable", () => {
      renderRow({ ...s3Source, installation_id: null } as StorageSource);
      expect(queryAction("Edit")).not.toBeInTheDocument();
      expect(queryAction("Rotate keys")).not.toBeInTheDocument();
    });

    it("hides Edit and Rotate keys for a disabled source", () => {
      renderRow({ ...s3Source, enabled: false, state: "disabled" } as StorageSource);
      expect(queryAction("Edit")).not.toBeInTheDocument();
      expect(queryAction("Rotate keys")).not.toBeInTheDocument();
    });

    it("hides every management action on a platform source in an organization scope", () => {
      scopeValue = "organization";
      renderRow(s3Source);
      for (const name of ["Edit", "Rotate keys", "Disable", "Remove"]) {
        expect(queryAction(name)).not.toBeInTheDocument();
      }
    });

    it("keeps the management actions on an organization source in an organization scope", () => {
      scopeValue = "organization";
      renderRow({ ...s3Source, owner_kind: "organization" } as StorageSource);
      for (const name of ["Test", "Edit", "Rotate keys", "Disable", "Remove"]) {
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
      fireEvent.click(screen.getByRole("button", { name: /confirm remove/i }));
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
      fireEvent.click(screen.getByRole("combobox", { name: "Release to upgrade to" }));
      const options = screen.getAllByRole("option");
      expect(options).toHaveLength(1);
      expect(options[0]).toHaveTextContent("bloem.storage.s3 0.3.2");
    });
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

  it("does not disable until the administrator confirms", () => {
    render(
      <ul>
        <StorageSourceRow source={s3Source} artifact={s3Artifact} />
      </ul>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Disable" }));
    expect(
      screen.getByText(
        "Disabling detaches this source from its libraries. It cannot be re-enabled from this page.",
      ),
    ).toBeInTheDocument();
    expect(disable).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Confirm disable" }));
    expect(disable).toHaveBeenCalledTimes(1);
    expect(disable).toHaveBeenCalledWith({ source: s3Source }, expect.any(Object));
  });

  it("keeps the source when the disable confirmation is dismissed", () => {
    render(
      <ul>
        <StorageSourceRow source={s3Source} artifact={s3Artifact} />
      </ul>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Disable" }));
    fireEvent.click(screen.getByRole("button", { name: "Keep" }));
    expect(screen.queryByRole("button", { name: "Confirm disable" })).not.toBeInTheDocument();
    expect(disable).not.toHaveBeenCalled();
  });
});
