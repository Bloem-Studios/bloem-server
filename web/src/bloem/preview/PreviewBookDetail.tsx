import type { ItemDetail } from "@/api/types";
import { Link } from "react-router";
import { Button } from "@/components/ui/button";
import DetailHero from "@/pages/ItemDetail/DetailHero";
import PageBack from "@/components/PageBack";
import MetadataBadges from "@/pages/ItemDetail/components/MetadataBadges";
import ScoreRow from "@/pages/ItemDetail/components/ScoreRow";
import { RelatedRail } from "@/pages/audiobooks/components/RelatedRail";
import { usePreviewMode } from "./usePreviewMode";
export default function PreviewBookDetail({
  item,
  libraryId,
  showAdvisoryAge,
}: {
  item: ItemDetail;
  libraryId?: number;
  showAdvisoryAge?: boolean;
}) {
  const mode = usePreviewMode();
  const audio = item.type === "audiobook";
  const authors = (audio ? item.audiobook?.authors : item.ebook?.authors) ?? [];
  const related = audio ? item.audiobook?.related : item.ebook?.related;
  const chapters = audio
    ? (item.versions ?? []).flatMap((version) => version.chapters ?? [])
    : (item.manga?.chapters ?? []);
  const context = audio ? "Audiobook" : item.type === "manga" ? "Manga" : "Ebook";
  return (
    <div>
      <DetailHero
        title={item.title}
        context={context}
        topNav={<PageBack />}
        studioLabel={item.ebook?.publisher || item.studios?.[0]}
        backdropUrl={item.backdrop_url}
        backdropThumbhash={item.backdrop_thumbhash}
        posterUrl={item.poster_url}
        posterThumbhash={item.poster_thumbhash}
        logoUrl={item.logo_url}
        metadata={
          <MetadataBadges
            year={item.year ? String(item.year) : undefined}
            contentRating={item.content_rating || undefined}
            advisoryAge={showAdvisoryAge ? (item.advisory_age ?? undefined) : undefined}
            advisorySource={item.advisory_source || undefined}
          />
        }
        scoreRow={<ScoreRow ratings={item.ratings} />}
        overview={item.overview}
        genres={item.genres}
        crewLine={
          authors.length ? `By ${authors.map((author) => author.name).join(", ")}` : undefined
        }
        genreHref={(genre) => {
          const params = new URLSearchParams({ genre });
          if (libraryId) {
            params.set("tab", "library");
            return `/library/${libraryId}?tab=library&genre=${encodeURIComponent(genre)}`;
          }
          params.set("source", "query");
          params.set("type", item.type);
          return `/catalog?${params.toString()}`;
        }}
        actions={
          <Button size="lg" onClick={() => mode.requestSubscription(item.content_id)}>
            {audio ? "Subscribe to listen" : "Subscribe to read"}
          </Button>
        }
      />
      <div className="page-shell detail-supporting-content space-y-10 py-10">
        {chapters.length > 0 && (
          <section>
            <h2 className="mb-4 text-xl font-semibold">Chapters</h2>
            <ul className="divide-y">
              {chapters.map((chapter, index) => (
                <li key={index} className="py-3">
                  {chapter.title || `Chapter ${index + 1}`}
                </li>
              ))}
            </ul>
          </section>
        )}
        {related && (
          <>
            <RelatedRail heading="Also by this author" items={related.also_by_author ?? []} />
            <RelatedRail heading="You might also like" items={related.similar ?? []} />
          </>
        )}
        {item.ebook?.series && (
          <section>
            <h2 className="mb-4 text-xl font-semibold">{item.ebook.series.name}</h2>
            {item.ebook.series.entries.map((entry) => (
              <Link
                className="mr-4 underline"
                key={entry.content_id}
                to={`/item/${encodeURIComponent(entry.content_id)}`}
              >
                {entry.title}
              </Link>
            ))}
          </section>
        )}
      </div>
    </div>
  );
}
