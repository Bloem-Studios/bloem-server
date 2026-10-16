import { useEffect, useState, type ComponentProps } from "react";
import { Link } from "react-router";
import type Original from "../../components/HeroBanner";
import { Button } from "@/components/ui/button";
import { usePreviewMode } from "./usePreviewMode";
import { itemCtaLabel } from "./wording";
export default function PreviewHero({
  items,
  maxSlides = 10,
  heightClassName,
  libraryId,
  reserveHeaderSpace,
}: ComponentProps<typeof Original>) {
  const { presentation, requestSubscription } = usePreviewMode();
  const [index, setIndex] = useState(0);
  const [paused, setPaused] = useState(false);
  const count = Math.min(items.length, maxSlides);
  useEffect(() => {
    if (paused || count < 2 || window.matchMedia("(prefers-reduced-motion: reduce)").matches)
      return;
    const timer = setInterval(() => setIndex((value) => (value + 1) % count), 8000);
    return () => clearInterval(timer);
  }, [paused, count]);
  const item = items[index % count];
  if (!item) return null;
  return (
    <section
      aria-label="Featured titles"
      className={`relative flex min-h-96 items-end overflow-hidden ${heightClassName ?? ""}`}
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      onFocusCapture={() => setPaused(true)}
    >
      {item.backdrop_url && (
        <img
          alt=""
          src={item.backdrop_url}
          className="absolute inset-0 h-full w-full object-cover"
        />
      )}
      <div className="from-background via-background/60 absolute inset-0 bg-gradient-to-t to-transparent" />
      <div className={`page-shell relative w-full py-12 ${reserveHeaderSpace ? "pt-28" : ""}`}>
        <h2 className="font-display max-w-4xl text-4xl font-extrabold sm:text-5xl">{item.title}</h2>
        {item.overview && (
          <p className="text-muted-foreground my-6 max-w-2xl text-base">{item.overview}</p>
        )}
        <div className="mt-6 flex gap-3">
          <Button onClick={() => requestSubscription(item.content_id)}>
            {itemCtaLabel(
              presentation,
              item.type === "audiobook"
                ? "listen"
                : ["ebook", "manga"].includes(item.type)
                  ? "read"
                  : "watch",
            )}
          </Button>
          <Button variant="outline" asChild>
            <Link
              to={`/item/${encodeURIComponent(item.content_id)}${libraryId ? `?libraryId=${libraryId}` : ""}`}
            >
              Details
            </Link>
          </Button>
        </div>
        {count > 1 && (
          <div className="mt-6 flex flex-wrap gap-2">
            <Button variant="ghost" size="sm" onClick={() => setPaused((value) => !value)}>
              {paused ? "Resume slideshow" : "Pause slideshow"}
            </Button>
            {items.slice(0, count).map((slide, position) => (
              <button
                key={slide.content_id}
                aria-label={`Show ${slide.title}`}
                aria-pressed={position === index}
                className="bg-muted rounded-full px-2 text-xs"
                onClick={() => {
                  setIndex(position);
                  setPaused(true);
                }}
              >
                {position + 1}
              </button>
            ))}
          </div>
        )}
      </div>
    </section>
  );
}
