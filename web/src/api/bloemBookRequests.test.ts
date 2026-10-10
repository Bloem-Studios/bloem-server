import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  bookRequestsApi,
  bookRequestState,
  type BookRequest,
  type BookRequestInput,
} from "./bloemBookRequests";
import {
  captureProfileRequestContext,
  setAccessToken,
  setRefreshToken,
  setProfileId,
  setProfileToken,
} from "./client";
import {
  activateAdminV2Context,
  captureAdminRequestContext,
  setAdminV2Token,
} from "./adminV2Client";

const input: BookRequestInput = { title: "Book", author: "Author", isbn: "", asin: "", note: "" };
const row: BookRequest = {
  id: "request-id",
  organization_id: "org-a",
  media_type: "audiobook",
  title: "Book",
  author: "Author",
  status: "approved",
  outcome: "active",
  requested_by_user_id: 1,
  requested_by_profile_id: "viewer",
  fulfilment: "awaiting_fulfilment",
  revision: 4,
  created_at: "2026-10-10T00:00:00Z",
  updated_at: "2026-10-10T00:00:00Z",
};

beforeEach(() => {
  setAccessToken("account-a");
  setRefreshToken(null);
  setProfileId("viewer");
  setProfileToken("viewer-pin");
  setAdminV2Token(null);
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe("audiobook request authority", () => {
  it("keeps one caller key and body across a retry", async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockRejectedValueOnce(new TypeError("connection reset"))
      .mockResolvedValueOnce(Response.json(row));
    vi.stubGlobal("fetch", fetchMock);
    await expect(
      bookRequestsApi.create(input, "stable-book-key-123", captureProfileRequestContext()),
    ).resolves.toEqual(row);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    for (const [url, init] of fetchMock.mock.calls) {
      expect(url).toBe("/api/bloem/v1/book-requests");
      expect(init?.body).toBe(JSON.stringify(input));
      expect(new Headers(init?.headers).get("Idempotency-Key")).toBe("stable-book-key-123");
      expect(new Headers(init?.headers).get("X-Profile-Id")).toBe("viewer");
    }
  });
  it("keeps an in-flight filing bound to its original profile after a household switch", async () => {
    const authority = captureProfileRequestContext();
    setProfileId("sibling");
    setProfileToken("sibling-pin");
    const fetchMock = vi.fn<typeof fetch>(async () => Response.json(row));
    vi.stubGlobal("fetch", fetchMock);
    await bookRequestsApi.create(input, "stable-book-key-123", authority);
    const headers = new Headers(fetchMock.mock.calls[0]?.[1]?.headers);
    expect(headers.get("X-Profile-Id")).toBe("viewer");
    expect(headers.get("X-Profile-Token")).toBe("viewer-pin");
  });
  it("refuses a queued filing after an account change", async () => {
    const authority = captureProfileRequestContext();
    setAccessToken("account-b");
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    await expect(
      bookRequestsApi.create(input, "stable-book-key-123", authority),
    ).rejects.toMatchObject({ name: "StaleApiRequestContextError" });
    expect(fetchMock).not.toHaveBeenCalled();
  });
  it("refuses missing profile authority", () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    expect(() => bookRequestsApi.create(input, "stable-book-key-123", null)).toThrow(
      /Select a profile/,
    );
    expect(fetchMock).not.toHaveBeenCalled();
  });
  it("uses the selected organization admin route and row revision", async () => {
    activateAdminV2Context("org-admin-token", "organization:org-a");
    const authority = captureAdminRequestContext("organization:org-a");
    const fetchMock = vi.fn<typeof fetch>(async () => Response.json(row));
    vi.stubGlobal("fetch", fetchMock);
    await bookRequestsApi.adminList(authority, 100);
    await bookRequestsApi.adminAction(row, "fulfil", "Handled manually", authority);
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "/api/bloem/v1/admin/organization/book-requests?limit=100&offset=100",
    );
    expect(fetchMock.mock.calls[1]?.[0]).toBe(
      "/api/bloem/v1/admin/organization/book-requests/request-id/fulfil",
    );
    expect(JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body))).toEqual({
      expected_revision: 4,
      reason: "Handled manually",
    });
    expect(new Headers(fetchMock.mock.calls[1]?.[1]?.headers).get("Authorization")).toBe(
      "Bearer org-admin-token",
    );
  });
  it("refuses an administrative mutation after organization switching", async () => {
    activateAdminV2Context("org-a-token", "organization:org-a");
    const authority = captureAdminRequestContext("organization:org-a");
    activateAdminV2Context("org-b-token", "organization:org-b");
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    await expect(bookRequestsApi.adminAction(row, "approve", "", authority)).rejects.toMatchObject({
      name: "AbortError",
    });
    expect(fetchMock).not.toHaveBeenCalled();
  });
  it("does not replay revision mutations after availability failures", async () => {
    activateAdminV2Context("org-admin-token", "organization:org-a");
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValue(Response.json({ error: "busy" }, { status: 503 }));
    vi.stubGlobal("fetch", fetchMock);
    await expect(
      bookRequestsApi.adminAction(
        row,
        "approve",
        "",
        captureAdminRequestContext("organization:org-a"),
      ),
    ).rejects.toMatchObject({ status: 503 });
    expect(fetchMock).toHaveBeenCalledOnce();
  });
  it("distinguishes waiting approval, awaiting fulfilment and manual completion", () => {
    expect(bookRequestState({ ...row, status: "pending" })).toBe("Awaiting approval");
    expect(bookRequestState(row)).toBe("Approved · awaiting fulfilment");
    expect(bookRequestState({ ...row, status: "completed", fulfilment: "manual" })).toBe(
      "Fulfilled by an admin",
    );
    expect(bookRequestState({ ...row, status: "completed", fulfilment: "library" })).toBe(
      "In your library",
    );
  });
});
