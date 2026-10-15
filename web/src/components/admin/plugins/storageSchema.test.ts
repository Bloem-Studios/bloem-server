import { afterEach, describe, expect, it, vi } from "vitest";
import { pruneConfigSchema, validateS3Connection, newProviderSourceId } from "./storageSchema";

const schema = {
  key: "connection",
  title: "S3 storage connection",
  required: true,
  json_schema: JSON.stringify({
    type: "object",
    properties: {
      source_id: { type: "string" },
      bucket: { type: "string" },
      state_path: { type: "string" },
      probe_key: { type: "string" },
    },
  }),
  admin_form: {
    fields: [
      {
        key: "source_id",
        label: "Source ID",
        control: "TEXT",
        required: false,
        secret: false,
        multiline: false,
      },
      {
        key: "bucket",
        label: "Bucket",
        control: "TEXT",
        required: true,
        secret: false,
        multiline: false,
      },
      {
        key: "state_path",
        label: "State path",
        control: "TEXT",
        required: false,
        secret: false,
        multiline: false,
      },
    ],
  },
} as const;

describe("pruneConfigSchema", () => {
  it("removes hidden fields from both the JSON schema and the admin form", () => {
    const out = pruneConfigSchema(schema as never, ["source_id", "state_path", "probe_key"]);
    expect(Object.keys(JSON.parse(out.json_schema).properties)).toEqual(["bucket"]);
    expect(out.admin_form?.fields.map((f) => f.key)).toEqual(["bucket"]);
  });
});

describe("validateS3Connection", () => {
  it("accepts empty or slash-terminated prefixes and https endpoints", () => {
    expect(
      validateS3Connection({ bucket: "media", prefix: "", endpoint: "https://s3.example.com" }),
    ).toEqual({});
    expect(
      validateS3Connection({
        bucket: "media",
        prefix: "movies/",
        endpoint: "https://s3.example.com",
      }),
    ).toEqual({});
  });
  it("flags a prefix without a trailing slash or with a leading slash", () => {
    expect(validateS3Connection({ bucket: "media", prefix: "movies" }).prefix).toMatch(/end with/);
    expect(validateS3Connection({ bucket: "media", prefix: "/movies/" }).prefix).toMatch(/start/);
  });
  it("flags http, paths and embedded credentials in the endpoint", () => {
    expect(
      validateS3Connection({ bucket: "media", endpoint: "http://s3.example.com" }).endpoint,
    ).toMatch(/https/);
    expect(
      validateS3Connection({ bucket: "media", endpoint: "https://s3.example.com/x" }).endpoint,
    ).toMatch(/path/);
    expect(
      validateS3Connection({ bucket: "media", endpoint: "https://u:p@s3.example.com" }).endpoint,
    ).toMatch(/credentials/);
  });
  it("requires a bucket and valid-looking bucket name", () => {
    expect(validateS3Connection({}).bucket).toBeTruthy();
    expect(validateS3Connection({ bucket: "A_B" }).bucket).toBeTruthy();
  });
  it("rejects bucket names with consecutive dots", () => {
    expect(validateS3Connection({ bucket: "my..media" }).bucket).toMatch(/consecutive dots/);
    expect(validateS3Connection({ bucket: "my.media" })).toEqual({});
  });
  it("rejects bucket names that look like an IPv4 address", () => {
    expect(validateS3Connection({ bucket: "192.168.1.10" }).bucket).toMatch(/IP address/);
    expect(validateS3Connection({ bucket: "192.168.1.example" })).toEqual({});
  });
  it("flags a query string or fragment in the endpoint", () => {
    expect(
      validateS3Connection({ bucket: "media", endpoint: "https://s3.example.com?x=1" }).endpoint,
    ).toMatch(/query or fragment/);
    expect(
      validateS3Connection({ bucket: "media", endpoint: "https://s3.example.com/#frag" }).endpoint,
    ).toMatch(/query or fragment/);
    expect(
      validateS3Connection({ bucket: "media", endpoint: "https://s3.example.com/?" }).endpoint,
    ).toMatch(/query or fragment/);
    expect(
      validateS3Connection({ bucket: "media", endpoint: "https://s3.example.com#" }).endpoint,
    ).toMatch(/query or fragment/);
  });
});

describe("newProviderSourceId", () => {
  afterEach(() => vi.unstubAllGlobals());
  it("matches the plugin source id pattern", () => {
    expect(newProviderSourceId("s3")).toMatch(/^s3_[a-f0-9]{32}$/);
  });
  it("still works where crypto.randomUUID is unavailable (plain HTTP)", () => {
    vi.stubGlobal("crypto", { getRandomValues: globalThis.crypto.getRandomValues.bind(crypto) });
    try {
      expect(globalThis.crypto.randomUUID).toBeUndefined();
      expect(newProviderSourceId("s3")).toMatch(/^s3_[a-f0-9]{32}$/);
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
