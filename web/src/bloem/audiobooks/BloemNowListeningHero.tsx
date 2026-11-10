import { useMemo, type MouseEvent } from "react";
import { Link } from "react-router";
import { Info, Pause, Play } from "lucide-react";
import type { ItemDetail, ResolvedSection } from "@/api/types";
import { useBloemT } from "@/bloem/i18n";
import ContinueWatchingCard from "@/components/ContinueWatchingCard";
import MediaCarousel from "@/components/MediaCarousel";
import NowListeningHero from "@/components/NowListeningHero";
import ViewTransitionLink from "@/components/ViewTransitionLink";
import { useCatalogItemDetail } from "@/hooks/queries/catalogRead";
import { useAmbientColor } from "@/hooks/useAmbientColor";
import { useOverlayPrefs } from "@/hooks/useOverlayPrefs";
import { useT } from "@/i18n";
import { buildChapterList, findChapterAt, totalAudiobookDuration } from "@/lib/audiobooks/chapters";
import { formatHoursMinutes, formatListeningTimeLeft } from "@/lib/audiobooks/duration";
import { audiobookFilesFromVersions } from "@/lib/audiobooks/files";
import { buildItemHref, buildMediaPlayHref } from "@/lib/mediaNavigation";
import { useSectionTitle } from "@/lib/sectionTitle";
import { decodeThumbhash } from "@/lib/thumbhash";
import { useAudiobookPlaybackController } from "@/pages/audiobooks/player/audiobookPlaybackContext";
import {
  itemAudiobookPresentations,
  itemForPresentation,
  ownsAudiobookItem,
} from "./presentations";
import { useAudiobookPresentationChoice } from "./useAudiobookPresentationChoice";

interface BloemNowListeningHeroProps {
  /** A resolved continue-listening section; items are in-progress audiobooks. */
  section: ResolvedSection;
  libraryId?: number;
}

/**
 * The audiobook library's resume deck. A deck book with more than one
 * recording, or a native one, resumes only the chosen recording; every other
 * book (and the deck before its detail loads) is Silo's NowListeningHero.
 * The deck reads the detail through the same query key as Silo's hero.
 */
export default function BloemNowListeningHero({ section, libraryId }: BloemNowListeningHeroProps) {
  const deck = section.items[0];
  const { data: fetchedDetail } = useCatalogItemDetail(deck?.content_id, libraryId);
  const detail =
    fetchedDetail?.type === "audiobook" && fetchedDetail.content_id === deck?.content_id
      ? fetchedDetail
      : undefined;
  const owned = useMemo(() => ownsAudiobookItem(itemAudiobookPresentations(detail)), [detail]);

  if (!deck || !detail || !owned) {
    return <NowListeningHero section={section} libraryId={libraryId} />;
  }
  return <PresentationNowListeningHero section={section} libraryId={libraryId} detail={detail} />;
}

function namesFromPeople(people: Array<{ name?: string }> | undefined): string | undefined {
  const names = (people ?? [])
    .map((person) => person.name?.trim())
    .filter(Boolean)
    .join(", ");
  return names || undefined;
}

/**
 * Silo's NowListeningHero (components/NowListeningHero.tsx) with its detail
 * replaced by the chosen recording's view of it. Keep the markup identical;
 * BloemNowListeningHero.parity.test.tsx compares the two.
 */
function PresentationNowListeningHero({
  section,
  libraryId,
  detail: fullDetail,
}: BloemNowListeningHeroProps & { detail: ItemDetail }) {
  // Silo's hero's own messages, so the two stay identical in every language.
  const t = useT();
  const deck = section.items[0]!;
  const rest = section.items.slice(1);
  const { prefs: overlayPrefs, quickActionMode } = useOverlayPrefs();
  const sectionTitle = useSectionTitle();
  const audiobookPlayback = useAudiobookPlaybackController();
  const { chosen } = useAudiobookPresentationChoice(fullDetail);
  // Changes with the language and as Bloem's messages load.
  const bt = useBloemT();
  // Until a recording is chosen the view has no tracks, so Resume falls
  // through to the item page, which decides once the capability answers.
  const detail = useMemo(
    () => itemForPresentation(fullDetail, chosen),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- a part without chapters gets a translated "Part N" title
    [chosen, fullDetail, bt],
  );
  useAmbientColor(deck.poster_thumbhash);

  const files = useMemo(() => audiobookFilesFromVersions(detail.versions), [detail]);
  // Built on each render so the fallback "Chapter N" labels it shows follow
  // the interface language.
  const chapters = buildChapterList(files);

  const detailProgress =
    detail.user_data && "position_seconds" in detail.user_data ? detail.user_data : undefined;
  const isActive = audiobookPlayback?.active?.contentId === deck.content_id;
  const isActivePlaying = isActive && Boolean(audiobookPlayback?.active?.playing);
  const livePosition = isActive ? (audiobookPlayback?.active?.currentTime ?? null) : null;
  const positionSeconds =
    livePosition ?? detailProgress?.position_seconds ?? deck.position_seconds ?? 0;
  const durationSeconds =
    detail.audiobook?.total_duration_seconds ||
    totalAudiobookDuration(files) ||
    deck.duration_seconds ||
    0;

  const author = namesFromPeople(detail.audiobook?.authors);
  const narrator = namesFromPeople(detail.audiobook?.narrators);
  const chapter = chapters.length > 0 ? findChapterAt(chapters, positionSeconds) : null;
  const progressPercent =
    durationSeconds > 0 ? Math.min((positionSeconds / durationSeconds) * 100, 100) : 0;
  const timeLeft = formatListeningTimeLeft(positionSeconds, durationSeconds);
  let chapterLine = durationSeconds > 0 ? formatHoursMinutes(durationSeconds) : "";
  if (chapter) {
    const index = chapter.index;
    const total = chapters.length;
    // Whether the chapter has a title of its own is decided on the English
    // data, as Silo's hero does, not by comparing translated text.
    const entry = chapters[chapter.index - 1];
    const englishLabel = entry && (entry.chapter.title || `Chapter ${entry.chapter.index + 1}`);
    const titled = englishLabel && englishLabel !== `Chapter ${chapter.index}`;
    chapterLine = titled
      ? t("web.audiobooks.hero.chapterPositionTitled", { index, total, chapter: chapter.label })
      : t("web.audiobooks.hero.chapterPosition", { index, total });
  }

  const playHref = buildMediaPlayHref({
    contentId: deck.content_id,
    type: deck.type,
    libraryId,
  });
  const itemHref = buildItemHref({ contentId: deck.content_id, libraryId });
  const thumbhashUrl = deck.poster_thumbhash ? decodeThumbhash(deck.poster_thumbhash) : "";

  const handleResumeClick = (event: MouseEvent<HTMLAnchorElement>) => {
    if (isActive) {
      event.preventDefault();
      audiobookPlayback?.toggleActivePlayback();
      return;
    }
    if (files.length > 0 && audiobookPlayback) {
      event.preventDefault();
      audiobookPlayback.startPlayback({
        contentId: deck.content_id,
        title: deck.title,
        author,
        narrator,
        posterUrl: deck.poster_url,
        files,
        initialPositionSeconds: positionSeconds > 0 ? positionSeconds : 0,
      });
    }
    // Without files yet, fall through to the detail page link, which
    // auto-starts the chosen recording via ?play=1.
  };

  const resumeLabel = isActivePlaying
    ? t("web.audiobooks.hero.pause")
    : positionSeconds > 0
      ? t("web.audiobooks.hero.resume")
      : t("web.audiobooks.hero.listen");

  return (
    <>
      <section
        className="relative -mt-[96px] w-full overflow-hidden sm:-mt-[104px]"
        aria-label={t("web.audiobooks.hero.ariaLabel")}
      >
        {/* The book's own cover, blurred, stands in for a backdrop and warms
            the section with the cover's palette alongside the ambient glow. */}
        <div
          className="absolute inset-0"
          style={
            thumbhashUrl
              ? { backgroundImage: `url(${thumbhashUrl})`, backgroundSize: "cover" }
              : undefined
          }
        >
          {deck.poster_url && (
            <img
              src={deck.poster_url}
              alt=""
              aria-hidden="true"
              className="h-full w-full scale-110 object-cover blur-3xl brightness-[0.45] saturate-[1.15]"
            />
          )}
        </div>
        <div className="hero-top-scrim" />
        <div className="hero-gradient-strong" />
        <div className="ambient-glow" />

        <div className="relative z-10 px-4 pt-28 pb-10 sm:px-6 sm:pt-32 sm:pb-12 lg:px-10 xl:px-12">
          <div className="flex max-w-[1380px] flex-col gap-6 sm:flex-row sm:items-center sm:gap-10">
            <ViewTransitionLink
              to={itemHref}
              className="block w-36 shrink-0 self-start sm:w-48 sm:self-center lg:w-56"
            >
              <div
                className="bg-muted aspect-square overflow-hidden rounded-2xl shadow-2xl"
                style={
                  thumbhashUrl
                    ? { backgroundImage: `url(${thumbhashUrl})`, backgroundSize: "cover" }
                    : undefined
                }
              >
                {deck.poster_url && (
                  <img
                    src={deck.poster_url}
                    alt={deck.title}
                    className="h-full w-full object-cover"
                  />
                )}
              </div>
            </ViewTransitionLink>

            <div className="min-w-0 flex-1">
              <p className="hero-eyebrow mb-3">
                <span className="hero-eyebrow-strong">{t("web.audiobooks.hero.eyebrow")}</span>
              </p>
              <h1
                className="font-display max-w-3xl text-3xl font-extrabold tracking-[-0.03em] text-balance sm:text-4xl lg:text-5xl"
                style={{ textShadow: "var(--hero-text-shadow, none)" }}
              >
                {deck.title}
              </h1>
              {(author || narrator) && (
                <p className="text-foreground/70 mt-2 max-w-2xl truncate text-sm sm:text-base">
                  {author && narrator
                    ? t("web.audiobooks.hero.authorAndNarrator", { author, narrator })
                    : narrator
                      ? t("web.audiobooks.hero.narratedBy", { narrator })
                      : author}
                </p>
              )}

              <div className="mt-6 max-w-xl">
                <div className="h-1.5 w-full overflow-hidden rounded-full bg-white/15">
                  <div
                    className="bg-primary h-full rounded-full transition-[width] duration-(--duration-normal)"
                    style={{ width: `${progressPercent}%` }}
                  />
                </div>
                <div className="mt-2 flex items-baseline justify-between gap-3 text-xs sm:text-sm">
                  <span className="text-foreground/60 truncate">{chapterLine}</span>
                  {timeLeft && <span className="text-foreground/85 shrink-0">{timeLeft}</span>}
                </div>
              </div>

              <div className="mt-6 flex flex-wrap items-center gap-3">
                <Link
                  to={playHref}
                  onClick={handleResumeClick}
                  className="pill pill-primary transition-colors duration-(--duration-fast)"
                >
                  {isActivePlaying ? (
                    <Pause className="h-4 w-4 fill-current" />
                  ) : (
                    <Play className="h-4 w-4 fill-current" />
                  )}
                  {resumeLabel}
                </Link>
                <ViewTransitionLink
                  to={itemHref}
                  className="pill pill-glass transition-colors duration-(--duration-fast)"
                >
                  <Info className="h-4 w-4" />
                  {t("web.audiobooks.hero.moreInfo")}
                </ViewTransitionLink>
              </div>
            </div>
          </div>
        </div>
      </section>

      {rest.length > 0 && (
        <MediaCarousel title={sectionTitle(section)}>
          {rest.map((item) => (
            <ContinueWatchingCard
              key={item.content_id}
              sectionItem={item}
              libraryId={libraryId}
              overlayPrefs={overlayPrefs}
              quickActionMode={quickActionMode}
              variant="poster"
            />
          ))}
        </MediaCarousel>
      )}
    </>
  );
}
