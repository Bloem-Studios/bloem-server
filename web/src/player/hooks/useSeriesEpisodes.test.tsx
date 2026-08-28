import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { EpisodeFile, EpisodeListItem, Season } from "@/api/types";
import { useSeriesEpisodes } from "./useSeriesEpisodes";

const catalogRead = vi.hoisted(() => ({
  fetchCatalogSeriesSeasons: vi.fn(),
  fetchCatalogSeasonEpisodes: vi.fn(),
}));

vi.mock("@/hooks/queries/catalogRead", () => catalogRead);

function season(seasonNumber: number): Season {
  return {
    content_id: `series-1-S${seasonNumber}`,
    season_number: seasonNumber,
    is_specials: false,
    title: `Season ${seasonNumber}`,
    overview: "",
    air_date: null,
    episode_count: 3,
    poster_url: "",
    poster_thumbhash: "",
  };
}

function file(fileID: number): EpisodeFile {
  return {
    file_id: fileID,
    resolution: "1080p",
    codec_video: "h264",
    hdr: false,
    audio_channels: 2,
    container: "mkv",
    file_size: 1_048_576,
  };
}

function episode(
  contentID: string,
  seasonNumber: number,
  episodeNumber: number,
  files?: EpisodeFile[],
): EpisodeListItem {
  return {
    content_id: contentID,
    season_number: seasonNumber,
    episode_number: episodeNumber,
    title: `Episode ${episodeNumber}`,
    overview: "",
    air_date: null,
    runtime: 45,
    still_url: "",
    still_thumbhash: "",
    files,
  };
}

function createHarness() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
}

describe("useSeriesEpisodes", () => {
  beforeEach(() => {
    catalogRead.fetchCatalogSeriesSeasons.mockReset();
    catalogRead.fetchCatalogSeasonEpisodes.mockReset();
  });

  it("builds navigation from playable rows while preserving cross-season order", async () => {
    catalogRead.fetchCatalogSeriesSeasons.mockResolvedValue({ seasons: [season(1), season(2)] });
    catalogRead.fetchCatalogSeasonEpisodes.mockImplementation(
      async (_seriesID: string, seasonNumber: number) => {
        if (seasonNumber === 1) {
          return {
            episodes: [
              episode("episode-current", 1, 1, [file(101)]),
              // The API keeps metadata rows but has already filtered their files
              // through the viewer's exact access policy. Empty means neither a
              // foreign/disabled file nor an otherwise unplayable row may become
              // a player navigation target.
              episode("episode-foreign-only", 1, 2),
              episode("episode-unplayable", 1, 3, []),
              // episode-missing is absent from the response altogether.
              episode("episode-mixed-allowed", 1, 5, [file(105)]),
            ],
          };
        }
        return { episodes: [episode("episode-next-season", 2, 1, [file(201)])] };
      },
    );

    const { result } = renderHook(() => useSeriesEpisodes("series-1", 1, 4), {
      wrapper: createHarness(),
    });

    await waitFor(() => expect(result.current.isLoading).toBe(false));

    expect(result.current.episodes.map((candidate) => candidate.contentId)).toEqual([
      "episode-current",
      "episode-mixed-allowed",
      "episode-next-season",
    ]);
    expect(result.current.episodes.map((candidate) => candidate.contentId)).not.toContain(
      "episode-foreign-only",
    );
    expect(result.current.episodes.map((candidate) => candidate.contentId)).not.toContain(
      "episode-unplayable",
    );
    expect(result.current.episodes.map((candidate) => candidate.contentId)).not.toContain(
      "episode-missing",
    );
    expect(catalogRead.fetchCatalogSeasonEpisodes).toHaveBeenCalledWith("series-1", 1, 4);
    expect(catalogRead.fetchCatalogSeasonEpisodes).toHaveBeenCalledWith("series-1", 2, 4);
  });
});
