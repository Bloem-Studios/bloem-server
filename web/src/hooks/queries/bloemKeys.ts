// Bloem query keys, kept out of the Silo-owned keys.ts.
export const liveTVKeys = {
  liveTVTuners: () => ["admin", "livetv", "tuners"] as const,
  liveTVChannels: (tunerId?: string) => ["admin", "livetv", "channels", tunerId ?? "all"] as const,
  liveTVGuideSources: () => ["admin", "livetv", "guide-sources"] as const,
  liveTVGuide: (params?: Record<string, unknown>) => ["livetv", "guide", params ?? {}] as const,
  liveTVRecordings: (status?: string) => ["livetv", "recordings", status ?? "all"] as const,
  liveTVSeriesRules: () => ["livetv", "series-rules"] as const,
};
