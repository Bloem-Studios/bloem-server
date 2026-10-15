import { describe, expect, it } from "vitest";

import { AdminV2ClientError } from "@/api/adminV2Client";

import {
  AMAZON_REGION_MESSAGE,
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
    expect(defaultRegionFor(endpoint)).toBe("");
    expect(defaultRevisionModeFor(endpoint)).toBe("strict");
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
  it("explains rejected keys and the approved-IP possibility", () => {
    expect(browseErrorMessage(err(422, "s3_access_denied"))).toBe(
      "The provider rejected these keys. Check the access key and secret key. Some providers also only allow connections from approved IP addresses — if yours does, ask them to approve this server.",
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
