import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { EpisodeRef, SeriesContext } from "../types";
import { useNextEpisode } from "./useNextEpisode";

function episode(contentId: string, seasonNumber: number, episodeNumber: number): EpisodeRef {
  return {
    contentId,
    seasonNumber,
    episodeNumber,
    title: contentId,
    runtime: 2_700,
  };
}

describe("useNextEpisode", () => {
  it("derives next navigation only from the supplied playable ordered set", () => {
    const episodes = [
      episode("episode-current", 1, 1),
      // Inaccessible S01E02 and a missing S01E03 are deliberately absent.
      episode("episode-mixed-allowed", 1, 4),
      episode("episode-next-season", 2, 1),
    ];
    const context: SeriesContext = {
      seriesId: "series-1",
      currentSeason: 1,
      currentEpisode: 1,
      episodes,
    };
    const onNavigate = vi.fn();
    const { result, rerender } = renderHook(
      ({ seriesContext }: { seriesContext: SeriesContext }) =>
        useNextEpisode(null, seriesContext, 0, onNavigate),
      { initialProps: { seriesContext: context } },
    );

    expect(result.current.nextEpisode?.contentId).toBe("episode-mixed-allowed");

    rerender({
      seriesContext: {
        ...context,
        currentEpisode: 4,
      },
    });

    expect(result.current.nextEpisode?.contentId).toBe("episode-next-season");
    result.current.skipToNext();
    expect(onNavigate).toHaveBeenCalledWith("episode-next-season");
  });
});
