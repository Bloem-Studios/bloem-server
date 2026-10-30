import { describe, expect, it } from "vitest";

import { S3_PROVIDERS, detectS3Provider, providerConnection, s3Provider } from "./s3Providers";

const R2_ACCOUNT = "0123456789abcdef0123456789abcdef";

describe("providerConnection", () => {
  it("builds each provider's address and signing region", () => {
    expect(providerConnection("backblaze", "us-west-004")).toEqual({
      endpoint: "https://s3.us-west-004.backblazeb2.com",
      region: "us-west-004",
    });
    expect(providerConnection("wasabi", "eu-central-2")).toEqual({
      endpoint: "https://s3.eu-central-2.wasabisys.com",
      region: "eu-central-2",
    });
    expect(providerConnection("r2", R2_ACCOUNT)).toEqual({
      endpoint: `https://${R2_ACCOUNT}.r2.cloudflarestorage.com`,
      region: "auto",
    });
    expect(providerConnection("amazon", "eu-west-1")).toEqual({
      endpoint: "https://s3.eu-west-1.amazonaws.com",
      region: "eu-west-1",
    });
    expect(providerConnection("digitalocean", "nyc3")).toEqual({
      endpoint: "https://nyc3.digitaloceanspaces.com",
      region: "us-east-1",
    });
    expect(providerConnection("hetzner", "fsn1")).toEqual({
      endpoint: "https://fsn1.your-objectstorage.com",
      region: "fsn1",
    });
  });

  it("accepts a pasted R2 address in place of the account ID", () => {
    expect(
      providerConnection("r2", ` https://${R2_ACCOUNT}.eu.r2.cloudflarestorage.com/movies `),
    ).toEqual({ endpoint: `https://${R2_ACCOUNT}.eu.r2.cloudflarestorage.com`, region: "auto" });
  });

  it("returns null while the location is incomplete or for Other", () => {
    expect(providerConnection("backblaze", "")).toBeNull();
    expect(providerConnection("backblaze", "us-west")).toBeNull();
    expect(providerConnection("r2", "abc")).toBeNull();
    expect(providerConnection("hetzner", "Fsn 1")).toBeNull();
    expect(providerConnection("other", "https://s3.example.com")).toBeNull();
  });
});

describe("detectS3Provider", () => {
  it("reads every provider back from the address it builds", () => {
    const locations: Record<string, string> = {
      backblaze: "eu-central-003",
      wasabi: "ap-northeast-1",
      r2: R2_ACCOUNT,
      amazon: "us-east-2",
      digitalocean: "ams3",
      hetzner: "hel1",
    };
    for (const [provider, location] of Object.entries(locations)) {
      const built = providerConnection(provider as never, location)!;
      expect(detectS3Provider(built.endpoint)).toEqual({ provider, location });
    }
  });

  it("knows Wasabi's short US East address and R2 addresses with a bucket path", () => {
    expect(detectS3Provider("s3.wasabisys.com")).toEqual({
      provider: "wasabi",
      location: "us-east-1",
    });
    expect(detectS3Provider(`https://${R2_ACCOUNT}.eu.r2.cloudflarestorage.com/media`)).toEqual({
      provider: "r2",
      location: `${R2_ACCOUNT}.eu`,
    });
  });

  it("calls anything else Other", () => {
    expect(detectS3Provider("https://minio.home.example:9000")).toEqual({
      provider: "other",
      location: "",
    });
    expect(detectS3Provider("")).toEqual({ provider: "other", location: "" });
  });
});

describe("S3_PROVIDERS", () => {
  it("uses each provider's own names for the keys", () => {
    expect(s3Provider("backblaze")).toMatchObject({
      accessKeyLabel: "keyID",
      secretKeyLabel: "applicationKey",
    });
    expect(s3Provider("r2")).toMatchObject({
      accessKeyLabel: "Access Key ID",
      secretKeyLabel: "Secret Access Key",
    });
    expect(S3_PROVIDERS.map((p) => p.id)).toEqual([
      "backblaze",
      "wasabi",
      "r2",
      "amazon",
      "digitalocean",
      "hetzner",
      "other",
    ]);
    for (const provider of S3_PROVIDERS) {
      expect(provider.keysHint.length).toBeGreaterThan(20);
      expect(Boolean(provider.location)).toBe(provider.id !== "other");
    }
  });
});
