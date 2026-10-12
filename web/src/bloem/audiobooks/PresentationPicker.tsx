import { useId } from "react";
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

function presentationLabels(presentations: AudiobookPresentation[]): Map<string, string> {
  const labels = new Map<string, string>();
  for (const native of [false, true]) {
    const group = presentations.filter((presentation) => presentation.native === native);
    const name = native ? "Remote copy (beta)" : "Local copy";
    group.forEach((presentation, index) => {
      labels.set(presentation.id, group.length > 1 ? `${name} ${index + 1}` : name);
    });
  }
  return labels;
}

function presentationHint(presentation: AudiobookPresentation, nativeAvailable: boolean): string {
  if (!presentation.complete) return "Incomplete: some parts are missing";
  if (presentation.native && !nativeAvailable) return "Not available on this server";
  const length = formatHoursMinutes(presentation.totalSeconds);
  return presentation.tracks.length > 1
    ? `${presentation.tracks.length} parts · ${length}`
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
  const headingId = useId();
  const labels = presentationLabels(presentations);

  return (
    <section className={cn("page-shell pt-6", className)} data-bloem-audiobook-picker="">
      <h2 id={headingId} className="text-foreground mb-3 text-sm font-semibold">
        Recording
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
            label={labels.get(presentation.id) ?? "Copy"}
            hint={presentationHint(presentation, nativeAvailable)}
            disabled={!isPlayablePresentation(presentation, nativeAvailable)}
          />
        ))}
      </RadioGroup>
      {!chosen ? (
        <p className="text-muted-foreground mt-2 text-xs">
          No copy of this book can play here right now.
        </p>
      ) : approximateResume ? (
        <p className="text-muted-foreground mt-2 text-xs">
          Your place was saved while listening to another copy, so this one resumes at about the
          same point.
        </p>
      ) : null}
    </section>
  );
}
