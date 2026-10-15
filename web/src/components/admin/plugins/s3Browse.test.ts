import { describe, expect, it } from "vitest";

import { AdminV2ClientError } from "@/api/adminV2Client";

import {
  AMAZON_REGION_MESSAGE,
  OUTCOME_UNKNOWN_MESSAGE,
  addErrorMessage,
  inferAmazonRegion,
  isOutcomeUnknown,
  withHttpsScheme,
  withTrailingSlash,
  BROWSE_UNSUPPORTED_MESSAGE,
  browseErrorMessage,
  defaultRegionFor,
  defaultRevisionModeFor,
  isAmazonEndpoint,
  isBrowseUnsupported,
} from "./s3Browse";

describe("Amazon detection and defaults", () => {
  it.each([
    "https://s3.amazonaws.com",
    "https://s3.us-west-2.amazonaws.com",
    "https://bucket.s3.eu-west-1.amazonaws.com/",
    "HTTPS://S3.AMAZONAWS.COM",
    "s3.amazonaws.com",
  ])("%s is Amazon", (endpoint) => {
    expect(isAmazonEndpoint(endpoint)).toBe(true);
    expect(defaultRevisionModeFor(endpoint)).toBe("strict");
  });

  it.each([
    ["https://s3.amazonaws.com", ""],
    ["https://s3.us-west-2.amazonaws.com", "us-west-2"],
    ["https://s3-eu-west-1.amazonaws.com", "eu-west-1"],
    ["https://bucket.s3.eu-west-1.amazonaws.com/", "eu-west-1"],
    ["https://s3.dualstack.ap-southeast-2.amazonaws.com", "ap-southeast-2"],
    ["https://s3-fips.us-gov-west-1.amazonaws.com", "us-gov-west-1"],
    ["https://s3.example.com", ""],
    ["https://evil-s3.us-west-2.amazonaws.com.example.com", ""],
  ])("infers the region of %s as %j", (endpoint, region) => {
    expect(inferAmazonRegion(endpoint)).toBe(region);
  });

  it("defaults an Amazon region to the inferred one, else blank", () => {
    expect(defaultRegionFor("https://s3.eu-west-1.amazonaws.com")).toBe("eu-west-1");
    expect(defaultRegionFor("https://s3.amazonaws.com")).toBe("");
  });

  it.each([
    "",
    "https://s3.example.com",
    "https://minio.lan:9000",
    "https://amazonaws.com.evil.example",
    "https://notamazonaws.com",
    "https://example.com/?x=.amazonaws.com",
    "not a url at all",
  ])("%j is a gateway", (endpoint) => {
    expect(isAmazonEndpoint(endpoint)).toBe(false);
    expect(defaultRegionFor(endpoint)).toBe("us-east-1");
    expect(defaultRevisionModeFor(endpoint)).toBe("checked-etag");
  });

  it("names the Amazon region message the form shows", () => {
    expect(AMAZON_REGION_MESSAGE).toBe("Amazon S3 needs a region");
  });
});

describe("browseErrorMessage", () => {
  const err = (status: number, code: string, message = "raw upstream text") =>
    new AdminV2ClientError(status, code, message);

  it("explains a rejected endpoint", () => {
    expect(browseErrorMessage(err(422, "s3_endpoint_rejected"))).toBe(
      "That address can't be used. It must be a public https:// address.",
    );
  });
  it("suggests typing the bucket when the keys cannot list buckets", () => {
    expect(browseErrorMessage(err(422, "s3_access_denied"))).toBe(
      "These keys can't list buckets. If your provider gave you a specific bucket name, type it instead.",
    );
    expect(browseErrorMessage(err(422, "s3_access_denied"), "folders")).toBe(
      "These keys can't list folders here. You can type the folder (prefix) instead.",
    );
  });
  it("explains an unreachable service for a backend error and for a network failure", () => {
    const text = "Couldn't reach the storage service. Check the address and try again.";
    expect(browseErrorMessage(err(502, "s3_backend_error"))).toBe(text);
    expect(browseErrorMessage(new TypeError("Failed to fetch"))).toBe(text);
    expect(browseErrorMessage(err(503, "unknown"))).toBe(text);
  });
  it("asks the administrator to wait on HTTP 429", () => {
    expect(browseErrorMessage(err(429, "rate_limited"))).toBe(
      "Too many requests. Wait a minute and try again.",
    );
    expect(browseErrorMessage(err(429, "unknown"))).toBe(
      "Too many requests. Wait a minute and try again.",
    );
  });
  it("falls back to typing on 404 and 405 from an older server", () => {
    for (const status of [404, 405]) {
      const e = err(status, "not_found");
      expect(browseErrorMessage(e)).toBe(BROWSE_UNSUPPORTED_MESSAGE);
      expect(isBrowseUnsupported(e)).toBe(true);
    }
    expect(browseErrorMessage(err(404, "unknown"))).toBe(BROWSE_UNSUPPORTED_MESSAGE);
    expect(isBrowseUnsupported(err(422, "s3_access_denied"))).toBe(false);
    expect(isBrowseUnsupported(new TypeError("Failed to fetch"))).toBe(false);
  });
  it("asks for the details to be checked on invalid_request", () => {
    expect(browseErrorMessage(err(400, "invalid_request"))).toBe(
      "Check the address and keys, then try again.",
    );
  });
  it("never repeats the server's own text", () => {
    const secret = "AKIAFAKEFAKEFAKE";
    for (const [status, code] of [
      [422, "s3_endpoint_rejected"],
      [422, "s3_access_denied"],
      [502, "s3_backend_error"],
      [400, "invalid_request"],
      [429, "rate_limited"],
      [404, "not_found"],
      [403, "forbidden"],
      [500, "internal"],
    ] as const) {
      expect(browseErrorMessage(err(status, code, `denied for ${secret}`))).not.toContain(secret);
    }
    expect(browseErrorMessage(new Error(`boom ${secret}`))).not.toContain(secret);
  });
});

describe("addErrorMessage", () => {
  const err = (status: number, code: string) =>
    new AdminV2ClientError(status, code, "raw server text AKIAFAKE");

  it.each([
    [
      422,
      "s3_endpoint_rejected",
      "That address can't be used. It must be a public https:// address.",
    ],
    [
      422,
      "s3_access_denied",
      "The provider rejected these keys for this bucket. Check the access key, the secret key and the bucket name.",
    ],
    [
      502,
      "s3_backend_error",
      "Couldn't reach the storage service. Check the address and try again.",
    ],
    [400, "invalid_request", "Check the address and keys, then try again."],
    [
      422,
      "artifact_rejected",
      "This storage plugin release can't be used. Ask your platform administrator.",
    ],
    [
      503,
      "native_storage_unavailable",
      "Storage isn't available on this server right now. Try again later.",
    ],
    [429, "unknown", "Too many requests. Wait a minute and try again."],
    [
      403,
      "insufficient_organization_authority",
      "You don't have permission to add storage sources.",
    ],
    [500, "internal", "Couldn't add this bucket. Try again."],
    [409, "conflict", "Couldn't add this bucket. Try again."],
  ])("maps %i %s to fixed text", (status, code, text) => {
    expect(addErrorMessage(err(status, code))).toBe(text);
  });

  it("never repeats the server's own text", () => {
    for (const [status, code] of [
      [422, "s3_access_denied"],
      [500, "internal"],
      [409, "x"],
    ] as const) {
      expect(addErrorMessage(err(status, code))).not.toContain("AKIAFAKE");
    }
    expect(addErrorMessage(new Error("boom AKIAFAKE"))).not.toContain("AKIAFAKE");
  });

  it("treats an unconfirmed write and a missing answer as outcome unknown", () => {
    expect(addErrorMessage(err(500, "mutation_outcome_unknown"))).toBe(OUTCOME_UNKNOWN_MESSAGE);
    expect(addErrorMessage(new TypeError("Failed to fetch"))).toBe(OUTCOME_UNKNOWN_MESSAGE);
    expect(isOutcomeUnknown(err(500, "mutation_outcome_unknown"))).toBe(true);
    expect(isOutcomeUnknown(new TypeError("Failed to fetch"))).toBe(true);
    expect(isOutcomeUnknown(err(422, "s3_access_denied"))).toBe(false);
    expect(isOutcomeUnknown(err(500, "internal"))).toBe(false);
  });
});

describe("typing leniency", () => {
  it("adds https:// to an address without a scheme and leaves others alone", () => {
    expect(withHttpsScheme("s3.example.com")).toBe("https://s3.example.com");
    expect(withHttpsScheme("minio.lan:9000")).toBe("https://minio.lan:9000");
    expect(withHttpsScheme("https://s3.example.com")).toBe("https://s3.example.com");
    expect(withHttpsScheme("http://s3.example.com")).toBe("http://s3.example.com");
    expect(withHttpsScheme("")).toBe("");
  });
  it("adds the trailing slash of a prefix but leaves a leading slash to be reported", () => {
    expect(withTrailingSlash("movies")).toBe("movies/");
    expect(withTrailingSlash("movies/10s")).toBe("movies/10s/");
    expect(withTrailingSlash("movies/")).toBe("movies/");
    expect(withTrailingSlash("/movies")).toBe("/movies");
    expect(withTrailingSlash("")).toBe("");
  });
});
