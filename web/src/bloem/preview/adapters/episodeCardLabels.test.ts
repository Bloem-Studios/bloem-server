// @vitest-environment node
// Bloem: every importer of Silo's episode card labels gets the adapter.
import { describe, expect, it } from "vitest";

import { buildEpisodeCardLabels } from "@/lib/episodeCardLabels";

const episode = {
  type: "episode",
  title: "Episode 3",
  series_title: "The Show",
  season_number: 1,
  episode_number: 3,
};

describe("episode card labels", () => {
  it("drops a provider's placeholder title under the series title", () => {
    const labels = buildEpisodeCardLabels(episode);
    expect(labels?.seriesTitle).toBe("The Show");
    expect(labels?.episodeTitle).toBeUndefined();
  });

  it("keeps a real episode title", () => {
    expect(buildEpisodeCardLabels({ ...episode, title: "The Pilot" })?.episodeTitle).toBe(
      "The Pilot",
    );
  });

  it("keeps a title that names another episode", () => {
    expect(buildEpisodeCardLabels({ ...episode, title: "Episode 4" })?.episodeTitle).toBe(
      "Episode 4",
    );
  });
});
