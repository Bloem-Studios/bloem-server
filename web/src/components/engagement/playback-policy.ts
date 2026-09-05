/** Natural chapter crossings only. Never fire on initial load, seek, or a stale sample. */
export class ChapterGate {
  private previous: { position: number; clock: number } | null = null;
  private used = false;
  sample(
    position: number,
    clock: number,
    playing: boolean,
    seeking: boolean,
    chapters: number[],
    eligible: boolean,
  ): boolean {
    const previous = this.previous;
    this.previous = playing && !seeking ? { position, clock } : null;
    if (this.used || !eligible || !previous || !playing || seeking) return false;
    const elapsed = (clock - previous.clock) / 1000,
      delta = position - previous.position;
    if (elapsed <= 0 || elapsed > 2 || delta <= 0 || delta > elapsed * 3 + 0.25) return false;
    if (!chapters.some((start) => start > 0 && previous.position < start && position >= start))
      return false;
    this.used = true;
    return true;
  }
}
