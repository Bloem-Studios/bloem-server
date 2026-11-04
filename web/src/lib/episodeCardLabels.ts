interface EpisodeCardLabelItem {
  type?: string;
  title?: string;
  series_title?: string;
  season_number?: number | null;
  episode_number?: number | null;
}

export interface EpisodeCardLabels {
  seriesTitle: string;
  episodeTitle?: string;
  episodeCode: string;
}

/** "S01E02", zero-padded like the Apple and Android episode cards. */
export function formatEpisodeCode(seasonNumber: number, episodeNumber: number): string {
  return `S${String(seasonNumber).padStart(2, "0")}E${String(episodeNumber).padStart(2, "0")}`;
}

export function buildEpisodeCardLabels(item: EpisodeCardLabelItem): EpisodeCardLabels | null {
  if (
    item.type !== "episode" ||
    item.season_number == null ||
    item.episode_number == null ||
    !item.title
  ) {
    return null;
  }

  const hasSeriesTitle = Boolean(item.series_title && item.series_title !== item.title);
  // Providers title an episode "Episode 3" until it gets a real name; under
  // the series title that only repeats the code line below it.
  const placeholderTitle = new RegExp(`^episode\\s+0*${item.episode_number}$`, "i").test(
    item.title.trim(),
  );

  return {
    seriesTitle: hasSeriesTitle ? item.series_title! : item.title,
    episodeTitle: hasSeriesTitle && !placeholderTitle ? item.title : undefined,
    episodeCode: formatEpisodeCode(item.season_number, item.episode_number),
  };
}
