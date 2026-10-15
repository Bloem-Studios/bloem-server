import { describe, expect, it } from "vitest";

import { parseS3Details } from "./s3Paste";

// Every value below is synthetic; none is a real key.
const FAKE_ACCESS = "FAKEACCESS0000EXAMPLE";
const FAKE_SECRET = "fakeSecret+0000/EXAMPLE0000000000000000=";

describe("parseS3Details", () => {
  it("reads an email-shaped message and ignores the noise lines", () => {
    const email = `Hi there,

Welcome aboard! Your object storage is ready to use.

Account details
---------------
S3 Endpoint: https://s3.storage.example.test
Access Key: ${FAKE_ACCESS}
Secret Key: ${FAKE_SECRET}
Whitelisted IP: Not configured
Region: eu-central-1

Keep your secret key safe: we cannot show it again.
Thanks, the Example Storage team`;
    expect(parseS3Details(email)).toEqual({
      endpoint: "https://s3.storage.example.test",
      accessKey: FAKE_ACCESS,
      secretKey: FAKE_SECRET,
      region: "eu-central-1",
    });
  });

  it.each([
    ["Endpoint: https://a.example.test", "https://a.example.test"],
    ["endpoint = https://a.example.test", "https://a.example.test"],
    ["ENDPOINT=https://a.example.test", "https://a.example.test"],
    ["S3 Endpoint - https://a.example.test", "https://a.example.test"],
    ["Endpoint URL: https://a.example.test", "https://a.example.test"],
    ["AWS_ENDPOINT_URL=https://a.example.test", "https://a.example.test"],
    ['"endpoint": "https://a.example.test",', "https://a.example.test"],
    ["Endpoint: 'https://a.example.test'.", "https://a.example.test"],
    ["Endpoint: <https://a.example.test>", "https://a.example.test"],
    ["**Endpoint:** https://a.example.test", "https://a.example.test"],
    ["- S3 endpoint: https://a.example.test/", "https://a.example.test"],
    ["Endpoint: https://a.example.test:9000", "https://a.example.test:9000"],
    ["Your S3 endpoint is: https://a.example.test", "https://a.example.test"],
  ])("endpoint line %j", (line, expected) => {
    expect(parseS3Details(line).endpoint).toBe(expected);
  });

  it("adds https:// to a bare host", () => {
    expect(parseS3Details("Endpoint: s3.a.example.test").endpoint).toBe(
      "https://s3.a.example.test",
    );
  });

  it("keeps an explicit plain-http endpoint so validation can reject it", () => {
    expect(parseS3Details("Endpoint: http://a.example.test").endpoint).toBe(
      "http://a.example.test",
    );
  });

  it.each([
    ["Access Key: AAA111", "AAA111"],
    ["Access Key ID: AAA111", "AAA111"],
    ["access_key_id = AAA111", "AAA111"],
    ["AWS_ACCESS_KEY_ID=AAA111", "AAA111"],
    ["export AWS_ACCESS_KEY_ID='AAA111'", "AAA111"],
    ["Your access key: “AAA111”", "AAA111"],
    ["Access Key ID: `AAA111`", "AAA111"],
  ])("access key line %j", (line, expected) => {
    expect(parseS3Details(line).accessKey).toBe(expected);
  });

  it.each([
    ["Secret Key: abc/DEF+123=", "abc/DEF+123="],
    ["Secret Access Key: abc/DEF+123=", "abc/DEF+123="],
    ["secret_access_key = abc/DEF+123=", "abc/DEF+123="],
    ["AWS_SECRET_ACCESS_KEY=abc/DEF+123=", "abc/DEF+123="],
    ["Access Key Secret: abc/DEF+123=", "abc/DEF+123="],
    ["Secret: abc/DEF+123=", "abc/DEF+123="],
  ])("secret key line %j", (line, expected) => {
    const parsed = parseS3Details(line);
    expect(parsed.secretKey).toBe(expected);
    // A secret key line is never mistaken for the access key.
    expect(parsed.accessKey).toBeUndefined();
  });

  it("is case-insensitive", () => {
    expect(parseS3Details("ACCESS KEY: A1\nsecret key: S1\nREGION: us-west-2")).toEqual({
      accessKey: "A1",
      secretKey: "S1",
      region: "us-west-2",
    });
  });

  it("reads an rclone-style config block", () => {
    const block = `[remote]
type = s3
provider = Other
access_key_id = ${FAKE_ACCESS}
secret_access_key = ${FAKE_SECRET}
endpoint = https://s3.storage.example.test
region = us-east-1`;
    expect(parseS3Details(block)).toEqual({
      endpoint: "https://s3.storage.example.test",
      accessKey: FAKE_ACCESS,
      secretKey: FAKE_SECRET,
      region: "us-east-1",
    });
  });

  it("reads an rclone-style single line of key=value pairs", () => {
    const line = `access_key_id=${FAKE_ACCESS} secret_access_key=${FAKE_SECRET} endpoint=https://s3.storage.example.test`;
    expect(parseS3Details(line)).toEqual({
      endpoint: "https://s3.storage.example.test",
      accessKey: FAKE_ACCESS,
      secretKey: FAKE_SECRET,
    });
  });

  it("reads a secret that contains equals signs and slashes whole", () => {
    expect(parseS3Details("secret_key=a=b/c==").secretKey).toBe("a=b/c==");
  });

  it("falls back to a URL on a line that mentions the endpoint", () => {
    expect(
      parseS3Details("Connect your tools to the endpoint https://s3.a.example.test today").endpoint,
    ).toBe("https://s3.a.example.test");
  });

  it("returns only what it found", () => {
    expect(parseS3Details("Access Key: A1")).toEqual({ accessKey: "A1" });
  });

  it("ignores placeholder values", () => {
    expect(parseS3Details("Region: Not set\nAccess Key: none\nEndpoint: n/a")).toEqual({});
  });

  it("returns nothing for text without connection details", () => {
    expect(parseS3Details("")).toEqual({});
    expect(parseS3Details("Whitelisted IP: Not configured\nHello there")).toEqual({});
    expect(parseS3Details("https://example.test/console")).toEqual({});
  });

  it("uses the first value when a field appears twice", () => {
    expect(parseS3Details("Access Key: FIRST\nAccess Key: SECOND").accessKey).toBe("FIRST");
  });

  it("handles Windows line endings", () => {
    expect(parseS3Details("Access Key: A1\r\nSecret Key: S1\r\n")).toEqual({
      accessKey: "A1",
      secretKey: "S1",
    });
  });
});
