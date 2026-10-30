/**
 * The S3 providers the Add form names, and how each one's address is built.
 * A newcomer picks their provider and gives only what it asks for (a region,
 * an account ID); the endpoint and signing region follow from that.
 */
import { inferAmazonRegion } from "./s3Browse";

export type S3ProviderId =
  | "backblaze"
  | "wasabi"
  | "r2"
  | "amazon"
  | "digitalocean"
  | "hetzner"
  | "other";

export interface S3Provider {
  id: S3ProviderId;
  name: string;
  accessKeyLabel: string;
  secretKeyLabel: string;
  /** Where the keys come from, in the provider's own words. */
  keysHint: string;
  /** What the provider's address is built from; absent for Other, which types an address. */
  location?: { label: string; placeholder: string; suggestions: string[]; hint: string };
}

export const S3_PROVIDERS: S3Provider[] = [
  {
    id: "backblaze",
    name: "Backblaze B2",
    accessKeyLabel: "keyID",
    secretKeyLabel: "applicationKey",
    keysHint:
      "In Backblaze, open Application Keys and add a key that can read your buckets. Copy the keyID and the applicationKey; Backblaze shows the applicationKey only once.",
    location: {
      label: "Region",
      placeholder: "us-west-004",
      suggestions: [
        "us-west-000",
        "us-west-001",
        "us-west-002",
        "us-west-004",
        "us-east-005",
        "eu-central-003",
        "ca-east-006",
      ],
      hint: "It is part of your bucket's Endpoint: s3.us-west-004.backblazeb2.com means us-west-004.",
    },
  },
  {
    id: "wasabi",
    name: "Wasabi",
    accessKeyLabel: "Access key",
    secretKeyLabel: "Secret key",
    keysHint:
      "In the Wasabi console, open Access Keys and create a key. Copy both keys; the secret key is shown only once.",
    location: {
      label: "Region",
      placeholder: "us-east-1",
      suggestions: [
        "us-east-1",
        "us-east-2",
        "us-central-1",
        "us-west-1",
        "us-west-2",
        "ca-central-1",
        "eu-central-1",
        "eu-central-2",
        "eu-west-1",
        "eu-west-2",
        "eu-west-3",
        "eu-south-1",
        "ap-northeast-1",
        "ap-northeast-2",
        "ap-southeast-1",
        "ap-southeast-2",
      ],
      hint: "Shown next to each bucket in the Wasabi console.",
    },
  },
  {
    id: "r2",
    name: "Cloudflare R2",
    accessKeyLabel: "Access Key ID",
    secretKeyLabel: "Secret Access Key",
    keysHint:
      "In Cloudflare, open R2, then Manage API tokens, and create a token that can read objects. Copy the Access Key ID and the Secret Access Key.",
    location: {
      label: "Account ID",
      placeholder: "32 letters and digits",
      suggestions: [],
      hint: "Shown on the R2 overview page. You can also paste the S3 API address from a bucket's settings.",
    },
  },
  {
    id: "amazon",
    name: "Amazon S3",
    accessKeyLabel: "Access key ID",
    secretKeyLabel: "Secret access key",
    keysHint:
      "In AWS, create an access key for an IAM user that can list and read your buckets. Copy the access key ID and the secret access key.",
    location: {
      label: "Region",
      placeholder: "us-east-1",
      suggestions: [
        "us-east-1",
        "us-east-2",
        "us-west-1",
        "us-west-2",
        "ca-central-1",
        "eu-west-1",
        "eu-west-2",
        "eu-west-3",
        "eu-central-1",
        "eu-north-1",
        "ap-southeast-1",
        "ap-southeast-2",
        "ap-northeast-1",
        "ap-south-1",
        "sa-east-1",
      ],
      hint: "The region your buckets are in, shown in the S3 console. Buckets in other regions can't be read.",
    },
  },
  {
    id: "digitalocean",
    name: "DigitalOcean Spaces",
    accessKeyLabel: "Access key",
    secretKeyLabel: "Secret key",
    keysHint:
      "In DigitalOcean, open Spaces Object Storage, then Access Keys, and create a key. Copy both keys; the secret is shown only once.",
    location: {
      label: "Datacenter",
      placeholder: "nyc3",
      suggestions: [
        "nyc3",
        "sfo2",
        "sfo3",
        "ams3",
        "fra1",
        "lon1",
        "tor1",
        "sgp1",
        "syd1",
        "blr1",
        "atl1",
      ],
      hint: "The start of your Space's address: nyc3.digitaloceanspaces.com means nyc3.",
    },
  },
  {
    id: "hetzner",
    name: "Hetzner Object Storage",
    accessKeyLabel: "Access key",
    secretKeyLabel: "Secret key",
    keysHint:
      "In the Hetzner Cloud Console, open Object Storage, then Security credentials, and generate credentials. Copy both keys; the secret is shown only once.",
    location: {
      label: "Location",
      placeholder: "fsn1",
      suggestions: ["fsn1", "nbg1", "hel1"],
      hint: "Falkenstein is fsn1, Nuremberg is nbg1 and Helsinki is hel1.",
    },
  },
  {
    id: "other",
    name: "Other or self-hosted",
    accessKeyLabel: "Access key",
    secretKeyLabel: "Secret key",
    keysHint:
      "Use the address and keys your storage provider or server administrator gave you, such as for MinIO or Garage.",
  },
];

export function s3Provider(id: S3ProviderId): S3Provider {
  return S3_PROVIDERS.find((p) => p.id === id)!;
}

const R2_HOST = /^([0-9a-f]{32}(?:\.(?:eu|fedramp))?)\.r2\.cloudflarestorage\.com$/;

const LOCATION_PATTERNS: Partial<Record<S3ProviderId, RegExp>> = {
  backblaze: /^[a-z]{2}-[a-z]+-\d{3}$/,
  wasabi: /^[a-z]{2}-[a-z]+-\d$/,
  r2: /^[0-9a-f]{32}(?:\.(?:eu|fedramp))?$/,
  amazon: /^[a-z]{2}(?:-gov|-iso[a-z]?)?-[a-z]+-\d+$/,
  digitalocean: /^[a-z]{3}\d$/,
  hetzner: /^[a-z]{3}\d$/,
};

function hostOf(text: string): string {
  const trimmed = text.trim();
  for (const candidate of [trimmed, `https://${trimmed}`]) {
    try {
      return new URL(candidate).hostname.toLowerCase();
    } catch {
      /* try the next form */
    }
  }
  return "";
}

/** The location as typed, tidied; an R2 address is reduced to its account ID. */
function normalizeLocation(id: S3ProviderId, raw: string): string {
  const text = raw.trim().toLowerCase();
  if (id === "r2" && text.includes("r2.cloudflarestorage.com")) {
    return R2_HOST.exec(hostOf(text))?.[1] ?? text;
  }
  return text;
}

export function providerConnection(
  id: S3ProviderId,
  rawLocation: string,
): { endpoint: string; region: string } | null {
  const location = normalizeLocation(id, rawLocation);
  if (!LOCATION_PATTERNS[id]?.test(location)) return null;
  switch (id) {
    case "backblaze":
      return { endpoint: `https://s3.${location}.backblazeb2.com`, region: location };
    case "wasabi":
      return { endpoint: `https://s3.${location}.wasabisys.com`, region: location };
    case "r2":
      return { endpoint: `https://${location}.r2.cloudflarestorage.com`, region: "auto" };
    case "amazon":
      return { endpoint: `https://s3.${location}.amazonaws.com`, region: location };
    case "digitalocean":
      // Spaces signs every datacenter as us-east-1; the address picks the datacenter.
      return { endpoint: `https://${location}.digitaloceanspaces.com`, region: "us-east-1" };
    case "hetzner":
      return { endpoint: `https://${location}.your-objectstorage.com`, region: location };
    default:
      return null;
  }
}

export function detectS3Provider(endpoint: string): {
  provider: S3ProviderId;
  location: string;
} {
  const host = hostOf(endpoint);
  const match = (pattern: RegExp) => pattern.exec(host)?.[1] ?? "";
  const found = (provider: S3ProviderId, location: string) =>
    LOCATION_PATTERNS[provider]?.test(location) ? { provider, location } : null;
  return (
    found("backblaze", match(/^s3\.([a-z0-9-]+)\.backblazeb2\.com$/)) ??
    (host === "s3.wasabisys.com" ? { provider: "wasabi" as const, location: "us-east-1" } : null) ??
    found("wasabi", match(/^s3\.([a-z0-9-]+)\.wasabisys\.com$/)) ??
    found("r2", match(R2_HOST)) ??
    found("amazon", inferAmazonRegion(endpoint)) ??
    found("digitalocean", match(/^([a-z0-9]+)\.digitaloceanspaces\.com$/)) ??
    found("hetzner", match(/^([a-z0-9]+)\.your-objectstorage\.com$/)) ?? {
      provider: "other",
      location: "",
    }
  );
}
