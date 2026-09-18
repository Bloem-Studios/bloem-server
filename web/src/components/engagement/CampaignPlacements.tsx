import { useState, type ReactNode } from "react";
import { useParams } from "react-router";
import { Button } from "@/components/ui/button";
import { CampaignCardView } from "./CampaignCardView";
import type { CampaignCard } from "./campaigns";
import { useCampaigns } from "./useCampaigns";

export function CampaignRail({
  cards,
  dismiss,
}: {
  cards: CampaignCard[];
  dismiss: (id: string) => Promise<void>;
}) {
  if (!cards.length) return null;
  return (
    <section className="space-y-4" aria-label="Messages from your server">
      <h2 className="text-xl font-semibold">From your server</h2>
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
        {cards.map((card) => (
          <CampaignCardView key={card.id} card={card} onDismiss={() => dismiss(card.id)} />
        ))}
      </div>
    </section>
  );
}
export function ItemCampaigns({ children }: { children: ReactNode }) {
  const { id } = useParams();
  const campaigns = useCampaigns("detail", id, Boolean(id));
  return (
    <>
      {children}
      {campaigns.cards.length > 0 && (
        <div className="page-shell py-6">
          <CampaignRail cards={campaigns.cards} dismiss={campaigns.dismiss} />
        </div>
      )}
    </>
  );
}

/** No wait field, timer, mandatory impression or disabled continue action. */
export function PrePlaybackCampaign({
  children,
  contentId,
  enabled,
  onCancel,
}: {
  children: ReactNode;
  contentId: string;
  enabled: boolean;
  onCancel: () => void;
}) {
  const [skipped, setSkipped] = useState(false);
  const campaigns = useCampaigns("pre_playback", contentId, enabled && !skipped);
  const bypass =
    !enabled || !campaigns.eligible || (!campaigns.query.isPending && !campaigns.cards.length);
  // Once content is admitted, a refetch or foreground transition must never
  // unmount a playing media engine to insert a newly arrived campaign.
  if (!skipped && bypass) setSkipped(true);
  if (skipped || bypass) return children;
  return (
    <section
      className="bg-background fixed inset-0 z-50 overflow-y-auto p-6"
      aria-label="Before playback"
    >
      <div className="mx-auto max-w-5xl space-y-6">
        <header className="flex flex-wrap items-center justify-between gap-4">
          <div>
            <h1 className="text-2xl font-semibold">Before you watch</h1>
            <p className="text-muted-foreground text-sm">
              You can continue immediately, even while messages load.
            </p>
          </div>
          <div className="flex flex-wrap gap-3">
            <Button autoFocus onClick={() => setSkipped(true)}>
              Continue to content
            </Button>
            <Button variant="outline" onClick={onCancel}>
              Back
            </Button>
          </div>
        </header>
        <CampaignRail cards={campaigns.cards} dismiss={campaigns.dismiss} />
      </div>
    </section>
  );
}
