import { useSearchParams } from "react-router";
import { Button } from "@/components/ui/button";

// One web route owns the request stores. The native Silo page remains a child of this
// Bloem wrapper, with all of its discovery and request behavior intact.
export function RequestMediaTabs({
  audiobooks = true,
  ebooks = false,
}: {
  audiobooks?: boolean;
  ebooks?: boolean;
}) {
  const [params, setParams] = useSearchParams();
  const parameter = params.get("kind");
  const kind = parameter === "audiobook" || parameter === "ebook" ? parameter : "video";
  return (
    <nav aria-label="Request media type" className="flex flex-wrap gap-2 px-4 pt-4 sm:px-6">
      {[
        { kind: "video", label: "Movies & series", visible: true },
        { kind: "audiobook", label: "Audiobooks", visible: audiobooks },
        { kind: "ebook", label: "Ebooks", visible: ebooks },
      ]
        .filter((item) => item.visible)
        .map((item) => (
          <Button
            key={item.label}
            size="sm"
            variant={kind === item.kind ? "default" : "outline"}
            aria-current={kind === item.kind ? "page" : undefined}
            onClick={() => {
              const next = new URLSearchParams(params);
              if (item.kind !== "video") next.set("kind", item.kind);
              else next.delete("kind");
              setParams(next);
            }}
          >
            {item.label}
          </Button>
        ))}
    </nav>
  );
}
