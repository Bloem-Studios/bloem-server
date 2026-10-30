import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AdminV2ClientError } from "@/api/adminV2Client";
import type { StorageArtifact, StorageSource } from "@/hooks/queries/admin/storageSources";

import { AddStorageSourceDialog } from "./AddStorageSourceDialog";

window.HTMLElement.prototype.scrollIntoView ??= () => {};

const discover = vi.fn();
const install = vi.fn();
const listBuckets = vi.fn();
const listFolders = vi.fn();
const resetDiscover = vi.fn();
const resetBuckets = vi.fn();
const resetFolders = vi.fn();
const toastSuccess = vi.hoisted(() => vi.fn());
const toastError = vi.hoisted(() => vi.fn());

vi.mock("sonner", () => ({ toast: { success: toastSuccess, error: toastError } }));
let scope: "platform" | "organization" = "platform";

vi.mock("@/hooks/queries/admin/storageSources", () => ({
  useDiscoverStorageSource: () => ({
    mutateAsync: discover,
    reset: resetDiscover,
    isPending: false,
  }),
  useInstallStorageSource: () => ({ mutate: install, isPending: false }),
  useListS3Buckets: () => ({ mutateAsync: listBuckets, reset: resetBuckets, isPending: false }),
  useListS3Folders: () => ({ mutateAsync: listFolders, reset: resetFolders, isPending: false }),
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
      bucket: { type: "string", minLength: 3, maxLength: 63 },
      prefix: { type: "string" },
      region: { type: "string" },
      endpoint: { type: "string" },
      revision_mode: { type: "string" },
      access_key: { type: "string" },
      secret_key: { type: "string" },
      session_token: { type: "string" },
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
      field("revision_mode", "Revision mode field"),
      field("access_key", "Access key", "PASSWORD", true),
      field("secret_key", "Secret key", "PASSWORD", true),
      field("session_token", "Session token", "PASSWORD", true),
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
/** The 0.4.0 release, whose schema lets the bucket be empty. */
const s3Schema040 = {
  ...s3Schema,
  json_schema: s3Schema.json_schema.replace('"minLength":3,', ""),
};
const artifacts040: StorageArtifact[] = [
  {
    ...artifacts[0]!,
    artifact_key: "s3-0.4.0-linux-amd64-y",
    version: "0.4.0",
    config_schema: [s3Schema040 as never],
  },
];
const R2_ACCOUNT = "0123456789abcdef0123456789abcdef";

const ENDPOINT = "https://s3.example.com";
const ACCESS = "AKIAFAKEACCESS0001";
const SECRET = "fakeSecretValue-9f2c/EXAMPLE";

type DialogProps = Partial<React.ComponentProps<typeof AddStorageSourceDialog>>;

function dialog(props: DialogProps, onOpenChange: () => void) {
  return (
    <MemoryRouter>
      <AddStorageSourceDialog
        artifacts={artifacts}
        sources={sources}
        open
        onOpenChange={onOpenChange}
        {...props}
      />
    </MemoryRouter>
  );
}
function open(props: DialogProps = {}) {
  const onOpenChange = vi.fn();
  const view = render(dialog(props, onOpenChange));
  return {
    onOpenChange,
    unmount: view.unmount,
    rerender: (next: DialogProps) => view.rerender(dialog({ ...props, ...next }, onOpenChange)),
  };
}
function fill(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}
/** Opens the S3 form without choosing a provider. */
function openS3Form(props: DialogProps = {}) {
  const handle = open(props);
  fireEvent.click(screen.getByRole("button", { name: /^S3$/ }));
  return handle;
}
const chooseProvider = (name: string) => fireEvent.click(screen.getByRole("radio", { name }));
/** Opens the S3 form on "Other or self-hosted", which asks for the address itself. */
function openS3(props: DialogProps = {}) {
  const handle = openS3Form(props);
  chooseProvider("Other or self-hosted");
  return handle;
}
function fillConnection(endpoint = ENDPOINT) {
  fill("S3 endpoint", endpoint);
  fill("Access key", ACCESS);
  fill("Secret key", SECRET);
}
const advanced = () => fireEvent.click(screen.getByRole("button", { name: "Advanced options" }));
const click = (name: string | RegExp) => fireEvent.click(screen.getByRole("button", { name }));

/** Opens the S3 form, connects and waits for the bucket checklist. */
async function connected(buckets: string[], endpoint = ENDPOINT) {
  listBuckets.mockResolvedValue(buckets);
  const handle = openS3();
  fillConnection(endpoint);
  click("Connect");
  await screen.findByText("Buckets");
  return handle;
}
const checkbox = (name: string) => screen.getByRole("checkbox", { name });

function deferred() {
  let resolve!: (value?: unknown) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

interface DiscoverCall {
  artifactKey: string;
  providerSourceId: string;
  config: { connection: Record<string, unknown> };
}
const discoverCall = (index: number) => discover.mock.calls[index]![0] as DiscoverCall;

describe("AddStorageSourceDialog", () => {
  beforeEach(() => {
    discover.mockReset();
    install.mockReset();
    listBuckets.mockReset();
    listFolders.mockReset();
    resetDiscover.mockReset();
    resetBuckets.mockReset();
    resetFolders.mockReset();
    toastSuccess.mockReset();
    toastError.mockReset();
    scope = "platform";
  });

  describe("which plugins are offered", () => {
    it("offers S3 and Bookwarehouse to platform admins", () => {
      open();
      expect(screen.getByRole("button", { name: /^S3$/ })).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Install Bookwarehouse" })).toBeInTheDocument();
    });

    it("offers S3 and Bookwarehouse to organization admins too", () => {
      scope = "organization";
      open();
      expect(screen.getByRole("button", { name: /^S3$/ })).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Install Bookwarehouse" })).toBeInTheDocument();
    });
  });

  describe("Bookwarehouse audiobooks", () => {
    it("installs a configured audiobook source rooted at its audiobook feed", () => {
      open();
      fireEvent.click(screen.getByRole("button", { name: "Audiobooks" }));
      const submit = screen.getByRole("button", { name: "Install audiobooks" });
      expect(submit).toBeDisabled();
      fireEvent.change(screen.getByLabelText("Bookwarehouse server URL"), {
        target: { value: " https://books.example " },
      });
      expect(submit).toBeDisabled();
      fireEvent.change(screen.getByLabelText("API key"), { target: { value: "key-1" } });
      fireEvent.click(submit);
      expect(install).toHaveBeenCalledTimes(1);
      const input = install.mock.calls[0]![0];
      expect(input.providerSourceId).toMatch(/^bookwarehouse_audio_[0-9a-f]{32}$/);
      expect(input).toEqual({
        artifactKey: expect.any(String),
        providerSourceId: input.providerSourceId,
        rootEntryId: "audio-root",
        config: {
          connection: {
            base_url: "https://books.example",
            api_key: "key-1",
            content: "audiobooks",
            source_id: input.providerSourceId,
            source_name: "Bookwarehouse audiobooks",
          },
        },
      });
    });

    it("goes back to the plugin list", () => {
      open();
      fireEvent.click(screen.getByRole("button", { name: "Audiobooks" }));
      fireEvent.click(screen.getByRole("button", { name: "Back" }));
      expect(screen.getByRole("button", { name: "Install Bookwarehouse" })).toBeInTheDocument();
    });
  });

  describe("guided by provider", () => {
    it("asks where the files are stored before anything else", () => {
      openS3Form();
      expect(
        screen.getByRole("group", { name: "Where are your files stored?" }),
      ).toBeInTheDocument();
      for (const name of [
        "Backblaze B2",
        "Wasabi",
        "Cloudflare R2",
        "Amazon S3",
        "DigitalOcean Spaces",
        "Hetzner Object Storage",
        "Other or self-hosted",
      ]) {
        expect(screen.getByRole("radio", { name })).not.toBeChecked();
      }
      expect(screen.queryByLabelText("Access key")).not.toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Connect" })).not.toBeInTheDocument();
    });

    it("builds a Backblaze address from the region and uses Backblaze's key names", async () => {
      listBuckets.mockResolvedValue(["movies"]);
      openS3Form();
      chooseProvider("Backblaze B2");
      expect(screen.queryByLabelText("S3 endpoint")).not.toBeInTheDocument();
      fill("Region", "us-west-004");
      fill("keyID", ACCESS);
      fill("applicationKey", SECRET);
      click("Connect");
      await screen.findByText("1 bucket found");
      expect(listBuckets).toHaveBeenCalledWith({
        endpoint: "https://s3.us-west-004.backblazeb2.com",
        region: "us-west-004",
        accessKey: ACCESS,
        secretKey: SECRET,
        sessionToken: "",
      });
    });

    it("says where to find the keys", () => {
      openS3Form();
      chooseProvider("Cloudflare R2");
      expect(screen.getByText("Where do I find these?")).toBeInTheDocument();
      expect(screen.getByText(/Manage API tokens/)).toBeInTheDocument();
      expect(screen.getByLabelText("Account ID")).toBeInTheDocument();
    });

    it("explains a region that doesn't look right instead of connecting", () => {
      openS3Form();
      chooseProvider("Backblaze B2");
      fill("Region", "us-west");
      fill("keyID", ACCESS);
      fill("applicationKey", SECRET);
      click("Connect");
      expect(screen.getByRole("alert")).toHaveTextContent(/Region: .*us-west-004/);
      expect(listBuckets).not.toHaveBeenCalled();
    });

    it("picks the provider from pasted details", () => {
      openS3Form();
      fireEvent.change(screen.getByLabelText("Paste your connection details"), {
        target: {
          value: `Endpoint: https://${R2_ACCOUNT}.r2.cloudflarestorage.com/media\nAccess Key: ${ACCESS}\nSecret Key: ${SECRET}`,
        },
      });
      expect(screen.getByRole("radio", { name: "Cloudflare R2" })).toBeChecked();
      expect(screen.getByLabelText("Account ID")).toHaveValue(R2_ACCOUNT);
      expect(screen.getByLabelText("Access Key ID")).toHaveValue(ACCESS);
    });
  });

  describe("all buckets", () => {
    async function connectedB2(
      buckets: string[],
      props: DialogProps = { artifacts: artifacts040 },
    ) {
      listBuckets.mockResolvedValue(buckets);
      openS3Form(props);
      chooseProvider("Backblaze B2");
      fill("Region", "us-west-004");
      fill("keyID", ACCESS);
      fill("applicationKey", SECRET);
      click("Connect");
      await screen.findByText(`${buckets.length} buckets found`);
    }

    it("covers every bucket by default with one source and no bucket", async () => {
      discover.mockResolvedValue(undefined);
      await connectedB2(["movies", "shows"]);
      expect(
        screen.getByRole("radio", { name: "All buckets these keys can see (recommended)" }),
      ).toBeChecked();
      expect(screen.getByText(/movies, shows/)).toBeInTheDocument();
      click("Add source");
      await screen.findByText("Added 1 source: Backblaze B2");
      expect(discover).toHaveBeenCalledTimes(1);
      const call = discoverCall(0);
      expect(call.artifactKey).toBe("s3-0.4.0-linux-amd64-y");
      expect(call.config.connection).toMatchObject({
        endpoint: "https://s3.us-west-004.backblazeb2.com",
        region: "us-west-004",
        source_name: "Backblaze B2",
        revision_mode: "checked-etag",
      });
      expect(call.config.connection).not.toHaveProperty("bucket");
      expect(call.config.connection).not.toHaveProperty("prefix");
      expect(screen.getByRole("link", { name: "Create a library from this" })).toHaveAttribute(
        "href",
        "/admin/libraries",
      );
    });

    it("still adds one source per chosen bucket", async () => {
      discover.mockResolvedValue(undefined);
      await connectedB2(["movies", "shows"]);
      chooseProvider("Choose buckets");
      fireEvent.click(checkbox("movies"));
      fireEvent.click(checkbox("shows"));
      click("Add 2 sources");
      await screen.findByText(/Added 2 sources/);
      expect(discoverCall(0).config.connection).toMatchObject({ bucket: "movies" });
      expect(discoverCall(1).config.connection).toMatchObject({ bucket: "shows" });
    });

    it("is not offered by a release that needs a bucket", async () => {
      await connectedB2(["movies", "shows"], {});
      expect(screen.queryByRole("radio", { name: /All buckets/ })).not.toBeInTheDocument();
      expect(screen.getByText("Buckets")).toBeInTheDocument();
    });

    it("takes an empty typed bucket as every bucket, without a prefix", async () => {
      discover.mockResolvedValue(undefined);
      openS3({ artifacts: artifacts040 });
      fillConnection();
      advanced();
      click("Type a bucket name instead");
      expect(screen.getByLabelText("Bucket (optional)")).toHaveValue("");
      fill("Prefix (optional)", "movies/");
      click("Add source");
      expect(screen.getByRole("alert")).toHaveTextContent(
        "A prefix needs a bucket. Leave it empty to use every bucket.",
      );
      fill("Prefix (optional)", "");
      click("Add source");
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(1));
      expect(discoverCall(0).config.connection).not.toHaveProperty("bucket");
      expect(discoverCall(0).config.connection.source_name).toBe("s3.example.com");
    });
  });

  describe("the form", () => {
    it("shows only the endpoint and the two keys until Advanced is opened", () => {
      openS3();
      expect(screen.getByLabelText("S3 endpoint")).toBeInTheDocument();
      expect(screen.getByLabelText("Access key")).toHaveAttribute("type", "password");
      expect(screen.getByLabelText("Secret key")).toHaveAttribute("type", "password");
      for (const hidden of [
        "Region",
        "Prefix (optional)",
        "Session token (optional)",
        "Source name",
        "Bucket",
        "Revision mode",
      ]) {
        expect(screen.queryByLabelText(hidden)).not.toBeInTheDocument();
      }
      expect(screen.getByRole("button", { name: "Advanced options" })).toHaveAttribute(
        "aria-expanded",
        "false",
      );
      advanced();
      for (const shown of [
        "Region",
        "Prefix (optional)",
        "Session token (optional)",
        "Source name",
        "Revision mode",
      ]) {
        expect(screen.getByLabelText(shown)).toBeInTheDocument();
      }
      expect(screen.getByLabelText("Session token (optional)")).toHaveAttribute("type", "password");
      // Server-managed fields are never asked for, and the bucket is chosen from the list.
      expect(screen.queryByLabelText("Source ID")).not.toBeInTheDocument();
      expect(screen.queryByLabelText("State path")).not.toBeInTheDocument();
      expect(screen.queryByLabelText("Bucket")).not.toBeInTheDocument();
    });

    it("explains the revision mode default in one sentence", () => {
      openS3();
      advanced();
      expect(screen.getByText(/Amazon S3 keeps object versions/)).toBeInTheDocument();
    });

    it("shows a retryable message when the plugin catalog is unavailable", () => {
      const retry = vi.fn();
      open({ artifacts: undefined, error: new Error("boom"), onRetry: retry });
      expect(screen.getByText(/plugin catalog unavailable/i)).toBeInTheDocument();
      fireEvent.click(screen.getByRole("button", { name: /retry/i }));
      expect(retry).toHaveBeenCalled();
    });

    it("shows a loading state on Retry while the catalog is refetching", () => {
      const retry = vi.fn();
      open({ artifacts: undefined, error: new Error("boom"), onRetry: retry, isRetrying: true });
      const button = screen.getByRole("button", { name: "Retrying…" });
      expect(button).toBeDisabled();
      fireEvent.click(button);
      expect(retry).not.toHaveBeenCalled();
    });
  });

  describe("pasting connection details", () => {
    const email = `Welcome!

S3 Endpoint: https://s3.paste.example.test
Access Key: ${ACCESS}
Secret Key: ${SECRET}
Whitelisted IP: Not configured`;

    it("fills the endpoint and keys and clears the pasted text", () => {
      openS3();
      fireEvent.change(screen.getByLabelText("Paste your connection details"), {
        target: { value: email },
      });
      expect(screen.getByLabelText("S3 endpoint")).toHaveValue("https://s3.paste.example.test");
      expect(screen.getByLabelText("Access key")).toHaveValue(ACCESS);
      expect(screen.getByLabelText("Secret key")).toHaveValue(SECRET);
      expect(screen.getByLabelText("Paste your connection details")).toHaveValue("");
      expect(screen.getByRole("button", { name: "Connect" })).toBeEnabled();
      // The note says what was filled in, never the values.
      expect(document.body.textContent).not.toContain(SECRET);
      expect(document.body.textContent).not.toContain(ACCESS);
    });

    it("also reads the text when the box loses focus", () => {
      openS3();
      const box = screen.getByLabelText("Paste your connection details");
      // Typed one character at a time, nothing is read until the box loses focus.
      const typed = "Access Key: AB";
      for (let length = 1; length <= typed.length; length++) {
        fireEvent.change(box, { target: { value: typed.slice(0, length) } });
      }
      expect(screen.getByLabelText("Access key")).toHaveValue("");
      fireEvent.blur(box);
      expect(screen.getByLabelText("Access key")).toHaveValue("AB");
    });

    it("never replaces what was typed with an empty result", () => {
      openS3();
      fillConnection();
      fireEvent.change(screen.getByLabelText("Paste your connection details"), {
        target: { value: "Region: eu-west-1\nnothing else useful here" },
      });
      expect(screen.getByLabelText("S3 endpoint")).toHaveValue(ENDPOINT);
      expect(screen.getByLabelText("Access key")).toHaveValue(ACCESS);
      expect(screen.getByLabelText("Secret key")).toHaveValue(SECRET);
    });

    it("keeps the text and says so when nothing could be read", () => {
      openS3();
      fireEvent.change(screen.getByLabelText("Paste your connection details"), {
        target: { value: "Hello, nothing to see here" },
      });
      expect(screen.getByText(/couldn't find an address or keys/i)).toBeInTheDocument();
      expect(screen.getByLabelText("Paste your connection details")).toHaveValue(
        "Hello, nothing to see here",
      );
    });

    it("uses a pasted region and keeps it when the endpoint changes", () => {
      openS3();
      fireEvent.change(screen.getByLabelText("Paste your connection details"), {
        target: { value: "Endpoint: https://s3.a.example.test\nRegion: eu-west-1" },
      });
      advanced();
      expect(screen.getByLabelText("Region")).toHaveValue("eu-west-1");
      fill("S3 endpoint", "https://s3.b.example.test");
      expect(screen.getByLabelText("Region")).toHaveValue("eu-west-1");
    });
  });

  describe("defaults from the endpoint", () => {
    it("starts a gateway on us-east-1 and Checked ETag", () => {
      openS3();
      fill("S3 endpoint", "https://minio.lan:9000");
      advanced();
      expect(screen.getByLabelText("Region")).toHaveValue("us-east-1");
      expect(screen.getByLabelText("Revision mode")).toHaveValue("checked-etag");
    });

    it("leaves the region blank and picks strict versioning for amazonaws.com", () => {
      openS3();
      fill("S3 endpoint", "https://s3.amazonaws.com");
      advanced();
      expect(screen.getByLabelText("Region")).toHaveValue("");
      expect(screen.getByLabelText("Revision mode")).toHaveValue("strict");
    });

    it("takes the region from an Amazon address that names one", () => {
      openS3();
      fill("S3 endpoint", "https://s3.eu-west-1.amazonaws.com");
      advanced();
      expect(screen.getByLabelText("Region")).toHaveValue("eu-west-1");
      expect(screen.getByLabelText("Revision mode")).toHaveValue("strict");
    });

    it("falls back to the region in the address when the region was cleared", async () => {
      listBuckets.mockResolvedValue(["media"]);
      openS3();
      fillConnection("https://s3.ap-southeast-2.amazonaws.com");
      advanced();
      fill("Region", "");
      click("Connect");
      await screen.findByText("Buckets");
      expect(listBuckets).toHaveBeenCalledWith(
        expect.objectContaining({ region: "ap-southeast-2" }),
      );
    });

    it("follows the endpoint back to the gateway defaults", () => {
      openS3();
      fill("S3 endpoint", "https://s3.amazonaws.com");
      fill("S3 endpoint", "https://s3.example.com");
      advanced();
      expect(screen.getByLabelText("Region")).toHaveValue("us-east-1");
      expect(screen.getByLabelText("Revision mode")).toHaveValue("checked-etag");
    });

    it("does not override a region or revision mode the administrator chose", () => {
      openS3();
      advanced();
      fill("Region", "garage");
      fireEvent.change(screen.getByLabelText("Revision mode"), { target: { value: "strict" } });
      fill("S3 endpoint", "https://s3.amazonaws.com");
      expect(screen.getByLabelText("Region")).toHaveValue("garage");
      expect(screen.getByLabelText("Revision mode")).toHaveValue("strict");
    });

    it("asks for a region on Amazon S3 and opens Advanced to show where", async () => {
      listBuckets.mockResolvedValue(["media"]);
      openS3();
      fillConnection("https://s3.amazonaws.com");
      click("Connect");
      expect(screen.getByRole("alert")).toHaveTextContent("Amazon S3 needs a region");
      expect(listBuckets).not.toHaveBeenCalled();
      expect(screen.getByLabelText("Region")).toBeInTheDocument();
      fill("Region", "us-west-2");
      click("Connect");
      await screen.findByText("Buckets");
      expect(listBuckets).toHaveBeenCalledWith(
        expect.objectContaining({ endpoint: "https://s3.amazonaws.com", region: "us-west-2" }),
      );
    });

    it("sends checked-etag to a gateway and leaves the mode out for Amazon S3", async () => {
      await connected(["media"]);
      click("Add source");
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(1));
      expect(discoverCall(0).config.connection).toMatchObject({
        revision_mode: "checked-etag",
        region: "us-east-1",
      });
    });

    it("omits the revision mode for strict versioning", async () => {
      listBuckets.mockResolvedValue(["media"]);
      openS3();
      fillConnection("https://s3.eu-west-1.amazonaws.com");
      advanced();
      fill("Region", "eu-west-1");
      click("Connect");
      await screen.findByText("Buckets");
      click("Add source");
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(1));
      expect(discoverCall(0).config.connection).not.toHaveProperty("revision_mode");
    });
  });

  describe("Connect", () => {
    it("is enabled only once the endpoint and both keys are entered", () => {
      openS3();
      const connect = screen.getByRole("button", { name: "Connect" });
      expect(connect).toBeDisabled();
      fill("S3 endpoint", ENDPOINT);
      fill("Access key", ACCESS);
      expect(connect).toBeDisabled();
      fill("Secret key", SECRET);
      expect(connect).toBeEnabled();
    });

    it("refuses an endpoint that is not https before calling the API", () => {
      openS3();
      fillConnection("http://s3.example.com");
      click("Connect");
      expect(screen.getByRole("alert")).toHaveTextContent(/must use https/);
      expect(listBuckets).not.toHaveBeenCalled();
    });

    it("lists the buckets with the entered details", async () => {
      await connected(["movies", "television"]);
      expect(listBuckets).toHaveBeenCalledWith({
        endpoint: ENDPOINT,
        region: "us-east-1",
        accessKey: ACCESS,
        secretKey: SECRET,
        sessionToken: "",
      });
      expect(checkbox("movies")).toBeInTheDocument();
      expect(checkbox("television")).toBeInTheDocument();
      expect(screen.queryByLabelText("Bucket")).not.toBeInTheDocument();
    });

    it("preselects the only bucket", async () => {
      await connected(["media"]);
      expect(checkbox("media")).toBeChecked();
      expect(screen.getByRole("button", { name: "Add source" })).toBeEnabled();
    });

    it("selects nothing by default when there are several, and offers Select all", async () => {
      await connected(["a-bucket", "b-bucket", "c-bucket"]);
      for (const name of ["a-bucket", "b-bucket", "c-bucket"])
        expect(checkbox(name)).not.toBeChecked();
      expect(screen.getByRole("button", { name: "Add source" })).toBeDisabled();
      click("Select all");
      for (const name of ["a-bucket", "b-bucket", "c-bucket"]) expect(checkbox(name)).toBeChecked();
      expect(screen.getByRole("button", { name: "Add 3 sources" })).toBeEnabled();
      click("Select none");
      expect(checkbox("a-bucket")).not.toBeChecked();
    });

    it("says so when the account has no buckets", async () => {
      await connected([]);
      expect(screen.getByText(/No buckets were found for these keys/)).toBeInTheDocument();
      // Keys limited to one bucket cannot list buckets: typing is offered right here, not only in Advanced.
      expect(screen.queryByRole("button", { name: "Advanced options", expanded: true })).toBeNull();
      expect(screen.getByRole("button", { name: "Add source" })).toBeDisabled();
      click("Type a bucket name instead");
      expect(screen.getByLabelText("Bucket")).toBeInTheDocument();
    });

    it("drops the list when the keys change afterwards", async () => {
      await connected(["a-bucket", "b-bucket"]);
      fill("Secret key", `${SECRET}x`);
      expect(screen.queryByText("Buckets")).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Connect" })).toBeInTheDocument();
    });

    it("ignores a listing that finishes after the details changed", async () => {
      const pending = deferred();
      listBuckets.mockReturnValue(pending.promise);
      openS3();
      fillConnection();
      click("Connect");
      fill("Secret key", "changed");
      await act(async () => {
        pending.resolve(["late"]);
      });
      expect(screen.queryByText("Buckets")).not.toBeInTheDocument();
    });
  });

  describe("listing errors", () => {
    const cases: [string, number, string, string][] = [
      [
        "s3_endpoint_rejected",
        422,
        "The S3 endpoint is not allowed",
        "That address can't be used. It must be a public https:// address.",
      ],
      [
        "s3_access_denied",
        422,
        "The S3 backend denied access",
        "These keys can't list buckets. If your provider gave you a specific bucket name, type it instead.",
      ],
      [
        "s3_backend_error",
        502,
        "The S3 backend request failed",
        "Couldn't reach the storage service. Check the address and try again.",
      ],
      ["rate_limited", 429, "slow down", "Too many requests. Wait a minute and try again."],
    ];

    it.each(cases)(
      "explains %s in plain language and offers Try again",
      async (code, status, raw, text) => {
        listBuckets.mockRejectedValueOnce(new AdminV2ClientError(status, code, `${raw} ${SECRET}`));
        openS3();
        fillConnection();
        click("Connect");
        const alert = await screen.findByRole("alert");
        expect(alert).toHaveTextContent(text);
        expect(alert).not.toHaveTextContent(raw);
        expect(document.body.textContent).not.toContain(SECRET);
        // The fields stay filled, and Try again asks once more.
        expect(screen.getByLabelText("S3 endpoint")).toHaveValue(ENDPOINT);
        expect(screen.getByLabelText("Secret key")).toHaveValue(SECRET);
        listBuckets.mockResolvedValueOnce(["media"]);
        click("Try again");
        await screen.findByText("Buckets");
        expect(listBuckets).toHaveBeenCalledTimes(2);
        expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      },
    );

    it("treats a network failure as an unreachable service", async () => {
      listBuckets.mockRejectedValueOnce(new TypeError("Failed to fetch"));
      openS3();
      fillConnection();
      click("Connect");
      expect(await screen.findByRole("alert")).toHaveTextContent(
        "Couldn't reach the storage service. Check the address and try again.",
      );
    });

    it("falls back to typing the bucket name on an older server", async () => {
      listBuckets.mockRejectedValueOnce(new AdminV2ClientError(404, "not_found", "no such route"));
      openS3();
      fillConnection();
      click("Connect");
      const alert = await screen.findByRole("alert");
      expect(alert).toHaveTextContent(
        "This server does not support browsing buckets; type the bucket name instead",
      );
      fireEvent.click(within(alert).getByRole("button", { name: "Type a bucket name instead" }));
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      fill("Bucket", "media");
      discover.mockResolvedValue(undefined);
      click("Add source");
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(1));
      expect(discoverCall(0).config.connection).toMatchObject({
        bucket: "media",
        access_key: ACCESS,
        secret_key: SECRET,
      });
    });

    it("offers typing from Advanced without a failure, and a way back", async () => {
      openS3();
      advanced();
      click("Type a bucket name instead");
      expect(screen.getByLabelText("Bucket")).toBeInTheDocument();
      click("Find buckets instead");
      expect(screen.queryByLabelText("Bucket")).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Connect" })).toBeInTheDocument();
    });
  });

  describe("adding one source", () => {
    it("discovers with a matching id, no state path, and the default name", async () => {
      await connected(["media"]);
      click("Add source");
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(1));
      const call = discoverCall(0);
      expect(call.artifactKey).toBe("s3-0.3.2-linux-amd64-x");
      expect(call.providerSourceId).toMatch(/^s3_[a-f0-9]{32}$/);
      expect(call.config.connection.source_id).toBe(call.providerSourceId);
      expect(call.config.connection).not.toHaveProperty("state_path");
      expect(call.config.connection).toMatchObject({
        endpoint: ENDPOINT,
        region: "us-east-1",
        bucket: "media",
        source_name: "media",
        access_key: ACCESS,
        secret_key: SECRET,
      });
      expect(call.config.connection).not.toHaveProperty("prefix");
    });

    it("uses the typed source name, trimmed, and the prefix", async () => {
      await connected(["media"]);
      advanced();
      fill("Prefix (optional)", "movies/");
      fill("Source name", " Movies ");
      click("Add source");
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(1));
      expect(discoverCall(0).config.connection).toMatchObject({
        prefix: "movies/",
        source_name: "Movies",
      });
    });

    it("defaults a blank name to bucket/prefix when a prefix is set", async () => {
      await connected(["media"]);
      advanced();
      fill("Prefix (optional)", "movies/");
      fill("Source name", "   ");
      click("Add source");
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(1));
      expect(discoverCall(0).config.connection.source_name).toBe("media/movies/");
    });

    it("sends a session token only when one was entered", async () => {
      listBuckets.mockResolvedValue(["media"]);
      openS3();
      fillConnection();
      advanced();
      fill("Session token (optional)", "TOKEN");
      click("Connect");
      await screen.findByText("Buckets");
      expect(listBuckets).toHaveBeenCalledWith(expect.objectContaining({ sessionToken: "TOKEN" }));
      click("Add source");
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(1));
      expect(discoverCall(0).config.connection).toHaveProperty("session_token", "TOKEN");
    });

    it("adds the trailing slash of a prefix itself and blocks only a leading slash", async () => {
      await connected(["media"]);
      advanced();
      fill("Prefix (optional)", "movies");
      click("Add source");
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(1));
      expect(discoverCall(0).config.connection).toMatchObject({
        prefix: "movies/",
        source_name: "media/movies/",
      });
    });

    it("blocks a prefix with a leading slash before calling the API", async () => {
      await connected(["media"]);
      advanced();
      fill("Prefix (optional)", "/movies/");
      click("Add source");
      expect(screen.getByRole("alert")).toHaveTextContent(/must not start with a slash/);
      expect(discover).not.toHaveBeenCalled();
    });

    it("opens Advanced to show a prefix problem", async () => {
      await connected(["media"]);
      advanced();
      fill("Prefix (optional)", "/movies/");
      advanced();
      expect(screen.queryByLabelText("Prefix (optional)")).not.toBeInTheDocument();
      click("Add source");
      expect(screen.getByLabelText("Prefix (optional)")).toBeInTheDocument();
    });

    it("asks for the bucket name and the keys when typing the bucket", () => {
      openS3();
      advanced();
      click("Type a bucket name instead");
      click("Add source");
      const alert = screen.getByRole("alert");
      expect(alert).toHaveTextContent("Bucket: Enter the bucket name.");
      expect(alert).toHaveTextContent("Access key: Enter the access key.");
      expect(alert).toHaveTextContent("Secret key: Enter the secret key.");
      expect(discover).not.toHaveBeenCalled();
    });

    it("warns about a duplicate bucket and prefix but still allows adding", async () => {
      const existing = {
        ...({} as StorageSource),
        plugin_id: "bloem.storage.s3",
        connection_summary: { bucket: "media", prefix: "movies/" },
      } as StorageSource;
      listBuckets.mockResolvedValue(["media"]);
      openS3({ sources: [existing] });
      fillConnection();
      click("Connect");
      await screen.findByText("Buckets");
      expect(screen.queryByText(/already exists/)).not.toBeInTheDocument();
      advanced();
      fill("Prefix (optional)", "movies/");
      expect(
        screen.getByText(/A source with this bucket and prefix already exists/),
      ).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Add source" })).toBeEnabled();
    });
  });

  describe("adding several buckets", () => {
    async function selectTwo() {
      const handle = await connected(["movies", "television", "music"]);
      fireEvent.click(checkbox("movies"));
      fireEvent.click(checkbox("television"));
      return handle;
    }

    it("creates one source per bucket, one after the other, with distinct ids", async () => {
      const first = deferred();
      const second = deferred();
      discover.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
      await selectTwo();
      click("Add 2 sources");
      expect(discover).toHaveBeenCalledTimes(1);
      expect(screen.getByText("movies: adding...")).toBeInTheDocument();
      expect(screen.getByText("television: waiting")).toBeInTheDocument();
      await act(async () => {
        first.resolve();
      });
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(2));
      await act(async () => {
        second.resolve();
      });
      const [a, b] = [discoverCall(0), discoverCall(1)];
      expect(a.providerSourceId).toMatch(/^s3_[a-f0-9]{32}$/);
      expect(b.providerSourceId).toMatch(/^s3_[a-f0-9]{32}$/);
      expect(a.providerSourceId).not.toBe(b.providerSourceId);
      expect(a.config.connection.source_id).toBe(a.providerSourceId);
      expect(b.config.connection.source_id).toBe(b.providerSourceId);
      expect(a.config.connection).toMatchObject({ bucket: "movies", source_name: "movies" });
      expect(b.config.connection).toMatchObject({
        bucket: "television",
        source_name: "television",
      });
      for (const call of [a, b]) {
        expect(call.config.connection).not.toHaveProperty("prefix");
        expect(call.config.connection).not.toHaveProperty("state_path");
        expect(call.config.connection).toMatchObject({ access_key: ACCESS, secret_key: SECRET });
      }
    });

    it("hides the folder browser, the prefix and the source name", async () => {
      await connected(["movies", "television"]);
      fireEvent.click(checkbox("movies"));
      expect(screen.getByRole("button", { name: "Browse folders" })).toBeInTheDocument();
      fireEvent.click(checkbox("television"));
      expect(screen.queryByRole("button", { name: "Browse folders" })).not.toBeInTheDocument();
      expect(screen.getByText("Each selected bucket is added from its root.")).toBeInTheDocument();
      advanced();
      expect(screen.queryByLabelText("Prefix (optional)")).not.toBeInTheDocument();
      expect(screen.queryByLabelText("Source name")).not.toBeInTheDocument();
    });

    it("ignores a prefix and name typed while one bucket was selected", async () => {
      discover.mockResolvedValue(undefined);
      await connected(["movies", "television"]);
      fireEvent.click(checkbox("movies"));
      advanced();
      fill("Prefix (optional)", "old/");
      fill("Source name", "Old name");
      fireEvent.click(checkbox("television"));
      click("Add 2 sources");
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(2));
      for (const index of [0, 1]) {
        expect(discoverCall(index).config.connection).not.toHaveProperty("prefix");
        expect(discoverCall(index).config.connection.source_name).not.toBe("Old name");
      }
    });

    it("clears the prefix when the selection changes to another bucket", async () => {
      await connected(["movies", "television"]);
      fireEvent.click(checkbox("movies"));
      advanced();
      fill("Prefix (optional)", "old/");
      fireEvent.click(checkbox("movies"));
      fireEvent.click(checkbox("television"));
      expect(screen.getByLabelText("Prefix (optional)")).toHaveValue("");
    });

    it("warns for each duplicate bucket at its root", async () => {
      const existing = (bucket: string) =>
        ({
          ...({} as StorageSource),
          plugin_id: "bloem.storage.s3",
          connection_summary: { bucket },
        }) as StorageSource;
      listBuckets.mockResolvedValue(["movies", "television", "music"]);
      openS3({ sources: [existing("movies"), existing("music")] });
      fillConnection();
      click("Connect");
      await screen.findByText("Buckets");
      fireEvent.click(checkbox("movies"));
      fireEvent.click(checkbox("television"));
      expect(screen.getByText(/already exists for movies\./)).toBeInTheDocument();
      expect(screen.getByText(/already exists for movies\./)).not.toHaveTextContent("music");
    });

    it("shows each result, goes on after a failure and retries only the failed bucket", async () => {
      discover
        .mockResolvedValueOnce(undefined)
        .mockRejectedValueOnce(new AdminV2ClientError(422, "artifact_rejected", "Bucket not found"))
        .mockResolvedValueOnce(undefined);
      await selectTwo();
      fireEvent.click(checkbox("music"));
      click("Add 3 sources");
      await screen.findByText(/television: failed — This storage plugin release can't be used/);
      expect(discover).toHaveBeenCalledTimes(3);
      expect(screen.getByText("movies: added")).toBeInTheDocument();
      expect(screen.getByText("music: added")).toBeInTheDocument();
      // The dialog stays open and the added buckets cannot be added twice.
      expect(screen.getByRole("dialog")).toBeInTheDocument();
      expect(checkbox("movies")).toBeDisabled();
      expect(checkbox("movies")).toBeChecked();
      expect(document.body.textContent).not.toContain("Bucket not found");
      const failedId = discoverCall(1).providerSourceId;

      discover.mockReset();
      discover.mockResolvedValue(undefined);
      click("Retry failed (1)");
      await screen.findByText(/Added 3 sources: movies, television, music/);
      expect(discover).toHaveBeenCalledTimes(1);
      expect(discoverCall(0).config.connection.bucket).toBe("television");
      // The server does not recognise a repeated id, so every attempt gets a fresh one.
      expect(discoverCall(0).providerSourceId).not.toBe(failedId);
      expect(discoverCall(0).providerSourceId).toMatch(/^s3_[a-f0-9]{32}$/);
    });

    it("does not warn that a bucket added a moment ago already exists", async () => {
      const added = {
        ...({} as StorageSource),
        plugin_id: "bloem.storage.s3",
        connection_summary: { bucket: "movies" },
      } as StorageSource;
      discover
        .mockResolvedValueOnce(undefined)
        .mockRejectedValueOnce(new AdminV2ClientError(422, "x", "nope"));
      const { rerender } = await connected(["movies", "television"]);
      click("Select all");
      click("Add 2 sources");
      await screen.findByText(/television: failed/);
      // The sources list refreshes after the first bucket was added.
      rerender({ sources: [added] });
      expect(screen.queryByText(/already exists/)).not.toBeInTheDocument();
    });

    it("stops adding when the dialog is closed part-way", async () => {
      const first = deferred();
      discover.mockReturnValueOnce(first.promise).mockResolvedValue(undefined);
      const { unmount } = await selectTwo();
      click("Add 2 sources");
      expect(discover).toHaveBeenCalledTimes(1);
      unmount();
      await act(async () => {
        first.resolve();
      });
      expect(discover).toHaveBeenCalledTimes(1);
    });
  });

  describe("browsing folders", () => {
    const root = {
      prefix: "",
      folders: [
        { name: "movies", prefix: "movies/" },
        { name: "television", prefix: "television/" },
      ],
      nextCursor: "",
    };

    it("walks down a level at a time and uses the folder as the prefix", async () => {
      listFolders
        .mockResolvedValueOnce(root)
        .mockResolvedValueOnce({
          prefix: "movies/",
          folders: [{ name: "10s", prefix: "movies/10s/" }],
          nextCursor: "",
        })
        .mockResolvedValueOnce({ prefix: "movies/10s/", folders: [], nextCursor: "" });
      await connected(["media"]);
      click("Browse folders");
      fireEvent.click(await screen.findByRole("button", { name: "movies" }));
      fireEvent.click(await screen.findByRole("button", { name: "10s" }));
      await waitFor(() => expect(listFolders).toHaveBeenCalledTimes(3));
      expect(listFolders.mock.calls[0]![0]).toMatchObject({
        endpoint: ENDPOINT,
        region: "us-east-1",
        accessKey: ACCESS,
        secretKey: SECRET,
        bucket: "media",
        prefix: "",
      });
      expect(listFolders.mock.calls[1]![0]).toMatchObject({ bucket: "media", prefix: "movies/" });
      expect(listFolders.mock.calls[2]![0]).toMatchObject({ prefix: "movies/10s/" });
      await screen.findByText("No subfolders here.");
      const nav = screen.getByRole("navigation", { name: "Folder path" });
      expect(within(nav).getByRole("button", { name: "media" })).toBeInTheDocument();
      expect(within(nav).getByRole("button", { name: "movies" })).toBeInTheDocument();
      expect(within(nav).getByText("10s")).toHaveAttribute("aria-current", "page");
    });

    it("sets the Prefix field from Use this folder, and the source is added from there", async () => {
      listFolders
        .mockResolvedValueOnce(root)
        .mockResolvedValueOnce({ prefix: "movies/", folders: [], nextCursor: "" });
      discover.mockResolvedValue(undefined);
      await connected(["media"]);
      click("Browse folders");
      fireEvent.click(await screen.findByRole("button", { name: "movies" }));
      await screen.findByText("No subfolders here.");
      const nav = screen.getByRole("navigation", { name: "Folder path" });
      expect(within(nav).getByText("movies")).toHaveAttribute("aria-current", "page");
      click("Use this folder");
      expect(screen.queryByRole("region", { name: "Folder browser" })).not.toBeInTheDocument();
      expect(screen.getByText("Folder: movies/")).toBeInTheDocument();
      advanced();
      expect(screen.getByLabelText("Prefix (optional)")).toHaveValue("movies/");
      click("Add source");
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(1));
      expect(discoverCall(0).config.connection).toMatchObject({
        bucket: "media",
        prefix: "movies/",
        source_name: "media/movies/",
      });
    });

    it("goes back up with the breadcrumbs and Use bucket root clears the prefix", async () => {
      listFolders
        .mockResolvedValueOnce(root)
        .mockResolvedValueOnce({ prefix: "movies/", folders: [], nextCursor: "" })
        .mockResolvedValueOnce(root);
      await connected(["media"]);
      advanced();
      fill("Prefix (optional)", "something/");
      click("Browse folders");
      fireEvent.click(await screen.findByRole("button", { name: "movies" }));
      await screen.findByText("No subfolders here.");
      const nav = screen.getByRole("navigation", { name: "Folder path" });
      fireEvent.click(within(nav).getByRole("button", { name: "media" }));
      await waitFor(() => expect(listFolders).toHaveBeenCalledTimes(3));
      expect(listFolders.mock.calls[2]![0]).toMatchObject({ prefix: "" });
      await screen.findByRole("button", { name: "television" });
      expect(screen.queryByRole("button", { name: "Use this folder" })).not.toBeInTheDocument();
      click("Use bucket root");
      expect(screen.getByLabelText("Prefix (optional)")).toHaveValue("");
      expect(screen.getByText("Folder: bucket root")).toBeInTheDocument();
    });

    it("loads more folders with the cursor and appends them", async () => {
      listFolders.mockResolvedValueOnce({ ...root, nextCursor: "page-2" }).mockResolvedValueOnce({
        prefix: "",
        folders: [{ name: "music", prefix: "music/" }],
        nextCursor: "",
      });
      await connected(["media"]);
      click("Browse folders");
      await screen.findByRole("button", { name: "movies" });
      click("Load more");
      await screen.findByRole("button", { name: "music" });
      expect(listFolders.mock.calls[1]![0]).toMatchObject({ prefix: "", cursor: "page-2" });
      expect(screen.getByRole("button", { name: "movies" })).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Load more" })).not.toBeInTheDocument();
    });

    it("explains a folder listing error and tries again", async () => {
      listFolders
        .mockRejectedValueOnce(new AdminV2ClientError(422, "s3_access_denied", `denied ${SECRET}`))
        .mockResolvedValueOnce(root);
      await connected(["media"]);
      click("Browse folders");
      const alert = await screen.findByRole("alert");
      expect(alert).toHaveTextContent(/These keys can't list folders here/);
      expect(alert).not.toHaveTextContent(SECRET);
      click("Try again");
      await screen.findByRole("button", { name: "movies" });
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    });

    it("can be closed without choosing", async () => {
      listFolders.mockResolvedValue(root);
      await connected(["media"]);
      click("Browse folders");
      await screen.findByRole("button", { name: "movies" });
      click("Cancel");
      expect(screen.queryByRole("region", { name: "Folder browser" })).not.toBeInTheDocument();
      expect(screen.getByText("Folder: bucket root")).toBeInTheDocument();
    });
  });

  describe("after every bucket is added", () => {
    async function addTwo() {
      discover.mockResolvedValue(undefined);
      const handle = await connected(["movies", "television"]);
      click("Select all");
      click("Add 2 sources");
      await screen.findByText(/Added 2 sources: movies, television/);
      return handle;
    }

    it("shows a success panel that links to the libraries page", async () => {
      await addTwo();
      const link = screen.getByRole("link", { name: "Create a library from these" });
      expect(link).toHaveAttribute("href", "/admin/libraries");
      expect(screen.getByRole("button", { name: "Add another" })).toBeInTheDocument();
    });

    it("names a single source by its source name", async () => {
      discover.mockResolvedValue(undefined);
      await connected(["media"]);
      advanced();
      fill("Source name", "My media");
      click("Add source");
      await screen.findByText("Added 1 source: My media");
    });

    it("leaves no key anywhere in the page once the sources are added", async () => {
      await addTwo();
      expect(screen.queryByLabelText("Access key")).not.toBeInTheDocument();
      expect(document.body.textContent).not.toContain(SECRET);
      expect(document.body.textContent).not.toContain(ACCESS);
      for (const input of Array.from(document.querySelectorAll("input, textarea"))) {
        expect((input as HTMLInputElement).value).not.toContain(SECRET);
      }
    });

    it("closes the dialog from the panel", async () => {
      const { onOpenChange } = await addTwo();
      click("Close");
      expect(onOpenChange).toHaveBeenCalledWith(false);
    });

    it("Add another starts again with empty keys", async () => {
      await addTwo();
      click("Add another");
      expect(screen.getByLabelText("S3 endpoint")).toHaveValue("");
      expect(screen.getByLabelText("Access key")).toHaveValue("");
      expect(screen.getByLabelText("Secret key")).toHaveValue("");
      expect(screen.getByRole("button", { name: "Connect" })).toBeDisabled();
      expect(screen.queryByText("Buckets")).not.toBeInTheDocument();
    });
  });

  describe("failed saves", () => {
    it("shows fixed text, never the server's, and raises one summary toast for a batch", async () => {
      discover
        .mockResolvedValueOnce(undefined)
        .mockRejectedValueOnce(
          new AdminV2ClientError(422, "s3_access_denied", `raw text ${SECRET} from the backend`),
        );
      await connected(["movies", "television"]);
      click("Select all");
      click("Add 2 sources");
      await screen.findByText(
        /television: failed — The provider rejected these keys for this bucket/,
      );
      expect(document.body.textContent).not.toContain("raw text");
      expect(document.body.textContent).not.toContain(SECRET);
      // Reported inline by the form; the hook is asked for no toast of its own.
      for (const call of discover.mock.calls) {
        expect(call[0]).toMatchObject({ silent: true });
      }
      expect(toastSuccess).not.toHaveBeenCalled();
      expect(toastError).toHaveBeenCalledTimes(1);
      expect(toastError).toHaveBeenCalledWith("1 of 2 storage sources couldn't be added");
    });

    it("raises a single success toast for a whole batch", async () => {
      discover.mockResolvedValue(undefined);
      await connected(["movies", "television"]);
      click("Select all");
      click("Add 2 sources");
      await screen.findByText(/Added 2 sources/);
      expect(toastSuccess).toHaveBeenCalledTimes(1);
      expect(toastSuccess).toHaveBeenCalledWith("Added 2 storage sources");
      expect(toastError).not.toHaveBeenCalled();
    });

    it("keeps the single-source toast text and fixed error text", async () => {
      discover.mockRejectedValueOnce(new AdminV2ClientError(500, "internal", "database exploded"));
      await connected(["media"]);
      click("Add source");
      await screen.findByText(/media: failed — Couldn't add this bucket. Try again./);
      expect(toastError).toHaveBeenCalledWith("Couldn't add the storage source");
      discover.mockResolvedValueOnce(undefined);
      click("Retry failed (1)");
      await screen.findByText(/Added 1 source: media/);
      expect(toastSuccess).toHaveBeenCalledWith("Storage source added");
    });

    it("holds back a bucket that may have been created and lets the administrator retry it", async () => {
      discover
        .mockRejectedValueOnce(
          new AdminV2ClientError(500, "mutation_outcome_unknown", "unknown outcome"),
        )
        .mockRejectedValueOnce(new AdminV2ClientError(422, "artifact_rejected", "x"));
      await connected(["movies", "television"]);
      click("Select all");
      click("Add 2 sources");
      await screen.findByText(/television: failed/);
      expect(
        screen.getByText(/movies: May have been created — check the source list before retrying/),
      ).toBeInTheDocument();
      // Only the plainly failed bucket is retried.
      expect(screen.getByRole("button", { name: "Retry failed (1)" })).toBeEnabled();
      discover.mockReset();
      discover.mockResolvedValue(undefined);
      click("Retry failed (1)");
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(1));
      expect(discoverCall(0).config.connection.bucket).toBe("television");
      expect(screen.queryByText(/Added/)).not.toBeInTheDocument();
      // Nothing else is tried until the administrator says so.
      expect(screen.getByRole("button", { name: "Add source" })).toBeDisabled();
      click("Retry movies anyway");
      discover.mockClear();
      click("Retry failed (1)");
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(1));
      expect(discoverCall(0).config.connection.bucket).toBe("movies");
      await screen.findByText(/Added 2 sources/);
    });

    it("treats a request that got no answer as possibly created", async () => {
      discover.mockRejectedValueOnce(new TypeError("Failed to fetch"));
      await connected(["media"]);
      click("Add source");
      await screen.findByText(/May have been created/);
      expect(screen.getByRole("button", { name: "Add source" })).toBeDisabled();
    });

    it("sends a fresh id for every attempt", async () => {
      discover.mockRejectedValueOnce(new AdminV2ClientError(500, "internal", "x"));
      discover.mockRejectedValueOnce(new AdminV2ClientError(500, "internal", "x"));
      await connected(["media"]);
      click("Add source");
      await screen.findByText(/media: failed/);
      click("Retry failed (1)");
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(2));
      expect(discoverCall(0).providerSourceId).not.toBe(discoverCall(1).providerSourceId);
      expect(discoverCall(1).config.connection.source_id).toBe(discoverCall(1).providerSourceId);
    });
  });

  describe("entered text is tidied", () => {
    it("trims the address, keys, token, bucket and prefix before using them", async () => {
      listBuckets.mockResolvedValue(["media"]);
      discover.mockResolvedValue(undefined);
      openS3();
      fill("S3 endpoint", "  https://s3.example.com \n");
      fill("Access key", ` ${ACCESS}\n`);
      fill("Secret key", `\t${SECRET} `);
      advanced();
      fill("Session token (optional)", " TOKEN ");
      fill("Region", " us-east-1 ");
      click("Connect");
      await screen.findByText("Buckets");
      expect(listBuckets).toHaveBeenCalledWith({
        endpoint: "https://s3.example.com",
        region: "us-east-1",
        accessKey: ACCESS,
        secretKey: SECRET,
        sessionToken: "TOKEN",
      });
      fill("Prefix (optional)", " movies/ ");
      click("Add source");
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(1));
      expect(discoverCall(0).config.connection).toMatchObject({
        endpoint: "https://s3.example.com",
        access_key: ACCESS,
        secret_key: SECRET,
        session_token: "TOKEN",
        region: "us-east-1",
        prefix: "movies/",
      });
    });

    it("trims a typed bucket name", async () => {
      discover.mockResolvedValue(undefined);
      openS3();
      fillConnection();
      advanced();
      click("Type a bucket name instead");
      fill("Bucket", "  media  ");
      click("Add source");
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(1));
      expect(discoverCall(0).config.connection.bucket).toBe("media");
    });

    it("adds https:// to an address typed without a scheme", async () => {
      listBuckets.mockResolvedValue(["media"]);
      openS3();
      fill("S3 endpoint", "s3.example.com:9000");
      fill("Access key", ACCESS);
      fill("Secret key", SECRET);
      click("Connect");
      await screen.findByText("Buckets");
      expect(listBuckets).toHaveBeenCalledWith(
        expect.objectContaining({ endpoint: "https://s3.example.com:9000" }),
      );
    });

    it("shows the scheme in the field once it is left, and the prefix slash too", () => {
      openS3();
      fill("S3 endpoint", "s3.example.com");
      fireEvent.blur(screen.getByLabelText("S3 endpoint"));
      expect(screen.getByLabelText("S3 endpoint")).toHaveValue("https://s3.example.com");
      advanced();
      fill("Prefix (optional)", "movies");
      fireEvent.blur(screen.getByLabelText("Prefix (optional)"));
      expect(screen.getByLabelText("Prefix (optional)")).toHaveValue("movies/");
    });
  });

  describe("keys that cannot list buckets", () => {
    it("offers typing the bucket in plain view after access is denied", async () => {
      listBuckets.mockRejectedValueOnce(
        new AdminV2ClientError(422, "s3_access_denied", "The S3 backend denied access"),
      );
      openS3();
      fillConnection();
      click("Connect");
      const alert = await screen.findByRole("alert");
      expect(alert).toHaveTextContent(
        "These keys can't list buckets. If your provider gave you a specific bucket name, type it instead.",
      );
      expect(screen.getByRole("button", { name: "Advanced options" })).toHaveAttribute(
        "aria-expanded",
        "false",
      );
      expect(
        within(alert).getByRole("button", { name: "Type a bucket name instead" }),
      ).toBeInTheDocument();
    });

    it("still lets the typed bucket's folders be browsed", async () => {
      listFolders
        .mockResolvedValueOnce({
          prefix: "",
          folders: [{ name: "movies", prefix: "movies/" }],
          nextCursor: "",
        })
        .mockResolvedValueOnce({ prefix: "movies/", folders: [], nextCursor: "" });
      discover.mockResolvedValue(undefined);
      openS3();
      fillConnection();
      advanced();
      click("Type a bucket name instead");
      // Nothing to browse until the bucket is named.
      expect(screen.queryByRole("button", { name: "Browse folders" })).not.toBeInTheDocument();
      fill("Bucket", "scoped-bucket");
      click("Browse folders");
      fireEvent.click(await screen.findByRole("button", { name: "movies" }));
      await screen.findByText("No subfolders here.");
      expect(listFolders.mock.calls[0]![0]).toMatchObject({
        bucket: "scoped-bucket",
        endpoint: ENDPOINT,
        accessKey: ACCESS,
        secretKey: SECRET,
      });
      click("Use this folder");
      expect(screen.getByLabelText("Prefix (optional)")).toHaveValue("movies/");
      click("Add source");
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(1));
      expect(discoverCall(0).config.connection).toMatchObject({
        bucket: "scoped-bucket",
        prefix: "movies/",
      });
    });

    it("closes the folder browser when the typed bucket name changes", async () => {
      listFolders.mockResolvedValue({ prefix: "", folders: [], nextCursor: "" });
      openS3();
      fillConnection();
      advanced();
      click("Type a bucket name instead");
      fill("Bucket", "first-bucket");
      click("Browse folders");
      await screen.findByText("No subfolders here.");
      fill("Bucket", "second-bucket");
      expect(screen.queryByRole("region", { name: "Folder browser" })).not.toBeInTheDocument();
      expect(listFolders).toHaveBeenCalledTimes(1);
    });
  });

  describe("errors can be found", () => {
    it("scrolls to the problems and moves focus there", () => {
      const scroll = vi.fn();
      window.HTMLElement.prototype.scrollIntoView = scroll;
      openS3();
      fillConnection("http://s3.example.com");
      click("Connect");
      const alert = screen.getByRole("alert");
      expect(scroll).toHaveBeenCalledWith({ block: "nearest" });
      expect(alert.parentElement).toBe(document.activeElement);
    });

    it("scrolls to and focuses a listing error and a failed result", async () => {
      const scroll = vi.fn();
      window.HTMLElement.prototype.scrollIntoView = scroll;
      listBuckets.mockRejectedValueOnce(new AdminV2ClientError(502, "s3_backend_error", "x"));
      openS3();
      fillConnection();
      click("Connect");
      const alert = await screen.findByRole("alert");
      await waitFor(() => expect(alert.parentElement).toBe(document.activeElement));
      expect(scroll).toHaveBeenCalled();

      scroll.mockClear();
      listBuckets.mockResolvedValue(["media"]);
      discover.mockRejectedValueOnce(new AdminV2ClientError(500, "internal", "x"));
      click("Try again");
      await screen.findByText("Buckets");
      click("Add source");
      await screen.findByText(/media: failed/);
      await waitFor(() => expect(scroll).toHaveBeenCalled());
      expect(screen.getByText(/media: failed/).closest("[tabindex='-1']")).toBe(
        document.activeElement,
      );
    });

    it("marks the fields a message is about as invalid and described by the message", async () => {
      openS3();
      fill("S3 endpoint", "https://s3.amazonaws.com");
      fill("Access key", ACCESS);
      fill("Secret key", SECRET);
      click("Connect");
      const region = screen.getByLabelText("Region");
      expect(region).toHaveAttribute("aria-invalid", "true");
      const describedBy = region.getAttribute("aria-describedby");
      expect(describedBy).toBeTruthy();
      expect(document.getElementById(describedBy!)).toHaveTextContent("Amazon S3 needs a region");
      expect(screen.getByLabelText("S3 endpoint")).not.toHaveAttribute("aria-invalid");
      fill("Region", "us-west-2");
      listBuckets.mockResolvedValue(["media"]);
      click("Connect");
      await screen.findByText("Buckets");
      expect(screen.getByLabelText("Region")).not.toHaveAttribute("aria-invalid");
      expect(screen.getByLabelText("Region")).not.toHaveAttribute("aria-describedby");
    });
  });

  describe("while sources are being added", () => {
    it("ignores Escape and hides the close button until the batch ends", async () => {
      const first = deferred();
      discover.mockReturnValueOnce(first.promise).mockResolvedValue(undefined);
      const { onOpenChange } = await connected(["movies", "television"]);
      click("Select all");
      click("Add 2 sources");
      await waitFor(() => expect(discover).toHaveBeenCalledTimes(1));
      const dialog = screen.getByRole("dialog");
      fireEvent.keyDown(dialog, { key: "Escape" });
      expect(onOpenChange).not.toHaveBeenCalled();
      expect(screen.queryByRole("button", { name: "Close" })).not.toBeInTheDocument();
      await act(async () => {
        first.resolve();
      });
      await screen.findByText(/Added 2 sources/);
      expect(screen.getByRole("button", { name: "Close" })).toBeInTheDocument();
      fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
      expect(onOpenChange).toHaveBeenCalledWith(false);
    });

    it("can be dismissed again after a partial failure", async () => {
      discover.mockRejectedValueOnce(new AdminV2ClientError(500, "internal", "x"));
      const { onOpenChange } = await connected(["media"]);
      click("Add source");
      await screen.findByText(/media: failed/);
      await waitFor(() => {
        fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
        expect(onOpenChange).toHaveBeenCalledWith(false);
      });
    });
  });

  describe("screen reader and keyboard flow", () => {
    it("moves focus to the bucket list and announces how many were found", async () => {
      await connected(["a-bucket", "b-bucket", "c-bucket"]);
      const group = screen.getByRole("group", { name: "Buckets" });
      expect(group).toBeInTheDocument();
      expect(document.activeElement).toBe(within(group).getByText("Buckets"));
      expect(screen.getByText("3 buckets found")).toBeInTheDocument();
    });

    it("has the live regions mounted before anything is announced in them", () => {
      openS3();
      const regions = Array.from(document.querySelectorAll("[role='status']"));
      // One for the bucket count and one for the per-bucket results.
      expect(regions.length).toBeGreaterThanOrEqual(2);
      expect(regions.some((r) => r.getAttribute("aria-live") === "polite")).toBe(true);
    });

    it("focuses the success block after the sources are added", async () => {
      discover.mockResolvedValue(undefined);
      await connected(["media"]);
      click("Add source");
      const message = await screen.findByText(/Added 1 source: media/);
      expect(message.closest("[role='status']")).toBe(document.activeElement);
    });
  });

  describe("dropping the keys", () => {
    it("resets the mutations once the sources are added", async () => {
      discover.mockResolvedValue(undefined);
      await connected(["media"]);
      resetDiscover.mockClear();
      resetBuckets.mockClear();
      click("Add source");
      await screen.findByText(/Added 1 source/);
      expect(resetDiscover).toHaveBeenCalled();
      expect(resetBuckets).toHaveBeenCalled();
    });

    it("resets them when the form is left", () => {
      openS3();
      resetDiscover.mockClear();
      resetBuckets.mockClear();
      click("Back");
      expect(resetDiscover).toHaveBeenCalled();
      expect(resetBuckets).toHaveBeenCalled();
    });

    it("empties the paste box when connecting, even if nothing in it could be read", async () => {
      listBuckets.mockResolvedValue(["media"]);
      openS3();
      fireEvent.change(screen.getByLabelText("Paste your connection details"), {
        target: { value: "some notes with a stray token abc123 that were not understood" },
      });
      fillConnection();
      click("Connect");
      await screen.findByText("Buckets");
      expect(screen.getByLabelText("Paste your connection details")).toHaveValue("");
      expect(document.body.textContent).not.toContain("abc123");
    });
  });

  it("never prints a key in the page while the form is used", async () => {
    discover.mockRejectedValue(
      new AdminV2ClientError(422, "artifact_rejected", "Bucket not found"),
    );
    await connected(["media"]);
    click("Add source");
    await screen.findByText(/media: failed/);
    expect(document.body.textContent).not.toContain(SECRET);
    expect(document.body.textContent).not.toContain(ACCESS);
  });
});
