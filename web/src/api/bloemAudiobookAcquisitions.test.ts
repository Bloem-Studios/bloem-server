import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  activateAdminV2Context,
  captureAdminRequestContext,
  clearAdminV2Context,
} from "./adminV2Client";
import { audiobookAcquisitionsApi } from "./bloemAudiobookAcquisitions";

beforeEach(() => activateAdminV2Context("synthetic-admin", "organization:org-a"));
afterEach(() => {
  clearAdminV2Context();
  vi.unstubAllGlobals();
});
const context = () => captureAdminRequestContext("organization:org-a");
describe("audiobook acquisition authority", () => {
  it("encodes filter and opaque cursor, passes cancellation and captured context", async () => {
    const fetchMock = vi.fn().mockResolvedValue(Response.json({ acquisitions: [] }));
    vi.stubGlobal("fetch", fetchMock);
    const abort = new AbortController();
    await audiobookAcquisitionsApi.list(context(), "review", "opaque/+", abort.signal);
    expect(fetchMock.mock.calls[0]![0]).toBe(
      "/api/bloem/v1/admin/organization/book-requests/acquisitions?state=review&limit=100&cursor=opaque%2F%2B",
    );
    abort.abort();
    expect(fetchMock.mock.calls[0]![1].signal.aborted).toBe(true);
  });
  it("pins exact candidate and expected revision without replaying unknown outcomes", async () => {
    const fetchMock = vi.fn().mockRejectedValue(new TypeError("Network interrupted"));
    vi.stubGlobal("fetch", fetchMock);
    await expect(
      audiobookAcquisitionsApi.approveCandidate("book/id", 7, "abook:123", context()),
    ).rejects.toThrow();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0]![0]).toBe(
      "/api/bloem/v1/admin/organization/book-requests/book%2Fid/approve-candidate",
    );
    expect(JSON.parse(fetchMock.mock.calls[0]![1].body)).toEqual({
      expected_revision: 7,
      candidate_ref: "abook:123",
    });
  });
  it("does not replay create or transfer it to another organization", async () => {
    const captured = context();
    const fetchMock = vi.fn().mockRejectedValue(new TypeError("Unknown create outcome"));
    vi.stubGlobal("fetch", fetchMock);
    const input = {
      name: "Synthetic",
      installation_id: 4,
      capability_id: "audiobooks",
      plugin_config: {},
      credentials: { ai_api_key: "synthetic" },
    };
    await expect(audiobookAcquisitionsApi.createIntegration(input, captured)).rejects.toThrow();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    activateAdminV2Context("other-admin", "organization:org-b");
    await expect(audiobookAcquisitionsApi.createIntegration(input, captured)).rejects.toMatchObject(
      { name: "AbortError" },
    );
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
  it("revision-checks acceptance and retry; binds only explicit download roots", async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(Response.json({})));
    vi.stubGlobal("fetch", fetchMock);
    await audiobookAcquisitionsApi.acceptDownload("book", 8, context());
    await audiobookAcquisitionsApi.retry("book", 9, context());
    await audiobookAcquisitionsApi.setLibrary("connection", 3, ["/synthetic/downloads"], context());
    expect(fetchMock.mock.calls.map((call) => JSON.parse(call[1].body))).toEqual([
      { expected_revision: 8 },
      { expected_revision: 9 },
      { library_id: 3, download_roots: ["/synthetic/downloads"] },
    ]);
  });
});
it("rejects a late read when administrative context generation changes", async () => {
  let release!: (response: Response) => void;
  vi.stubGlobal(
    "fetch",
    vi.fn(
      () =>
        new Promise<Response>((resolve) => {
          release = resolve;
        }),
    ),
  );
  const pending = audiobookAcquisitionsApi.detail("book", context());
  activateAdminV2Context("new-admin", "organization:org-a");
  release(Response.json({ request: { id: "book" } }));
  await expect(pending).rejects.toMatchObject({ name: "AbortError" });
});
