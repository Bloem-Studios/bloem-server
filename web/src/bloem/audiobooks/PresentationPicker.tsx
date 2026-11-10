import { useId } from "react";
import { useBloemT, type BloemTranslate } from "@/bloem/i18n";
import { RadioCardItem, RadioGroup } from "@/components/ui/radio-group";
import { formatHoursMinutes } from "@/lib/audiobooks/duration";
import { cn } from "@/lib/utils";
import { isPlayablePresentation, type AudiobookPresentation } from "./presentations";

interface PresentationPickerProps {
  presentations: AudiobookPresentation[];
  chosen: AudiobookPresentation | undefined;
  nativeAvailable: boolean;
  onChoose: (presentationId: string) => void;
  /** The stored position was written while listening to a different copy. */
  approximateResume?: boolean;
  className?: string;
}

function presentationLabels(
  bt: BloemTranslate,
  presentations: AudiobookPresentation[],
): Map<string, string> {
  const labels = new Map<string, string>();
  for (const native of [false, true]) {
    const group = presentations.filter((presentation) => presentation.native === native);
    group.forEach((presentation, index) => {
      const number = index + 1;
      const label =
        group.length > 1
          ? native
            ? bt("bloem.audiobooks.picker.remoteCopyNumbered", { number })
            : bt("bloem.audiobooks.picker.localCopyNumbered", { number })
          : native
            ? bt("bloem.audiobooks.picker.remoteCopy")
            : bt("bloem.audiobooks.picker.localCopy");
      labels.set(presentation.id, label);
    });
  }
  return labels;
}

function presentationHint(
  bt: BloemTranslate,
  presentation: AudiobookPresentation,
  nativeAvailable: boolean,
): string {
  if (!presentation.complete) return bt("bloem.audiobooks.picker.incomplete");
  if (presentation.native && !nativeAvailable) return bt("bloem.audiobooks.picker.unavailable");
  const length = formatHoursMinutes(presentation.totalSeconds);
  return presentation.tracks.length > 1
    ? bt("bloem.audiobooks.picker.partsAndLength", { count: presentation.tracks.length, length })
    : length;
}

/**
 * Lets the listener pick which recording of the book to use. Each choice is a
 * whole presentation; the player never mixes or swaps them.
 */
export default function PresentationPicker({
  presentations,
  chosen,
  nativeAvailable,
  onChoose,
  approximateResume = false,
  className,
}: PresentationPickerProps) {
  const bt = useBloemT();
  const headingId = useId();
  const labels = presentationLabels(bt, presentations);

  return (
    <section className={cn("page-shell pt-6", className)} data-bloem-audiobook-picker="">
      <h2 id={headingId} className="text-foreground mb-3 text-sm font-semibold">
        {bt("bloem.audiobooks.picker.heading")}
      </h2>
      <RadioGroup
        aria-labelledby={headingId}
        value={chosen?.id ?? ""}
        onValueChange={onChoose}
        className="grid max-w-2xl gap-2 sm:grid-cols-2"
      >
        {presentations.map((presentation) => (
          <RadioCardItem
            key={presentation.id}
            value={presentation.id}
            label={labels.get(presentation.id) ?? bt("bloem.audiobooks.picker.copyFallback")}
            hint={presentationHint(bt, presentation, nativeAvailable)}
            disabled={!isPlayablePresentation(presentation, nativeAvailable)}
          />
        ))}
      </RadioGroup>
      {!chosen ? (
        <p className="text-muted-foreground mt-2 text-xs">
          {bt("bloem.audiobooks.picker.noPlayableCopy")}
        </p>
      ) : approximateResume ? (
        <p className="text-muted-foreground mt-2 text-xs">
          {bt("bloem.audiobooks.picker.approximateResume")}
        </p>
      ) : null}
    </section>
  );
}
