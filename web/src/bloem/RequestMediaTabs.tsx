import { useSearchParams } from "react-router";
import { Button } from "@/components/ui/button";

// One web route owns both stores. The native Silo page remains a child of this
// Bloem wrapper, with all of its discovery and request behavior intact.
export function RequestMediaTabs() {
  const [params, setParams] = useSearchParams();
  const audio = params.get("kind") === "audiobook";
  return (
    <nav aria-label="Request media type" className="flex flex-wrap gap-2 px-4 pt-4 sm:px-6">
      {[
        { audio: false, label: "Movies & series" },
        { audio: true, label: "Audiobooks" },
      ].map((item) => (
        <Button
          key={item.label}
          size="sm"
          variant={audio === item.audio ? "default" : "outline"}
          aria-current={audio === item.audio ? "page" : undefined}
          onClick={() => {
            const next = new URLSearchParams(params);
            if (item.audio) next.set("kind", "audiobook");
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
