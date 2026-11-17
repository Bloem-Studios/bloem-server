import { buildEpisodeCardLabels as buildSiloEpisodeCardLabels } from "@/lib/episodeCardLabels";

export * from "@/lib/episodeCardLabels";

/**
 * Silo's episode card labels without a placeholder title. Providers title an
 * episode "Episode 3" until it gets a real name; under the series title that
 * only repeats the code line below it.
 */
export function buildEpisodeCardLabels(
  item: Parameters<typeof buildSiloEpisodeCardLabels>[0],
): ReturnType<typeof buildSiloEpisodeCardLabels> {
  const labels = buildSiloEpisodeCardLabels(item);
  if (!labels?.episodeTitle) return labels;
  const placeholderTitle = new RegExp(`^episode\\s+0*${item.episode_number}$`, "i").test(
    labels.episodeTitle.trim(),
  );
  return placeholderTitle ? { ...labels, episodeTitle: undefined } : labels;
}
