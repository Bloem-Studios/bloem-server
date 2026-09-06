import { afterEach, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ v2: vi.fn() }));
vi.mock("@/api/v2/request", () => ({ v2: mocks.v2 }));

import { fetchDownloadedSubtitles, searchSubtitles } from "./subtitles";

afterEach(() => mocks.v2.mockReset());

it("sends search IDs as strings and preserves cancellation and partial results", async () => {
  const signal = new AbortController().signal;
  const response = { results: [{ id: "provider-result" }], warnings: ["Provider unavailable"] };
  mocks.v2.mockResolvedValue(response);
  expect(await searchSubtitles({ media_file_id: 42, languages: ["en"] }, { signal })).toBe(
    response,
  );
  expect(mocks.v2).toHaveBeenCalledWith("POST /api/v2/subtitles/search", {
    body: { media_file_id: "42", languages: ["en"] },
    signal,
  });
});

it("adapts stored IDs only at the existing numeric track selector boundary", async () => {
  mocks.v2.mockResolvedValue({ subtitles: [{ id: "7", media_file_id: "42", language: "en" }] });
  expect(await fetchDownloadedSubtitles(42)).toEqual([
    { id: 7, media_file_id: 42, language: "en" },
  ]);
  expect(mocks.v2).toHaveBeenCalledWith("GET /api/v2/subtitles/{media_file_id}", {
    path: { media_file_id: "42" },
    signal: undefined,
  });
});

it.each(["not-numeric", "9007199254740993", "0", "-1"])(
  "refuses to silently coerce stored ID %s into a different track",
  async (id) => {
    mocks.v2.mockResolvedValue({ subtitles: [{ id, media_file_id: "42" }] });
    await expect(fetchDownloadedSubtitles(42)).rejects.toThrow("Unsupported subtitle identifier");
  },
);
