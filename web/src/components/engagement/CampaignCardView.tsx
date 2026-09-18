import { useState } from "react";
import { Button } from "@/components/ui/button";
import { campaignImage, campaignLink, type CampaignCard } from "./campaigns";

export function CampaignCardView({
  card,
  onDismiss,
}: {
  card: CampaignCard;
  onDismiss?: () => Promise<void>;
}) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const href = campaignLink(card.cta?.url || card.deeplink);
  const image = campaignImage(card.image_url);
  return (
    <article className="border-border bg-card min-w-0 overflow-hidden rounded-xl border">
      {image && (
        <img
          className="aspect-video w-full object-cover"
          src={image}
          alt=""
          loading="lazy"
          referrerPolicy="no-referrer"
        />
      )}
      <div className="space-y-3 p-4">
        <p className="text-muted-foreground text-xs">{card.kicker || "From your server"}</p>
        <h3 className="text-lg font-semibold break-words">{card.headline}</h3>
        {card.subtitle && <p className="text-muted-foreground text-sm">{card.subtitle}</p>}
        <div className="flex flex-wrap gap-2">
          {href && (
            <Button variant="outline" asChild>
              <a href={href} rel="noopener noreferrer">
                {card.cta?.label || "Learn more"}
              </a>
            </Button>
          )}
          {card.dismissible && onDismiss && (
            <Button
              variant="ghost"
              disabled={pending}
              onClick={() => {
                setPending(true);
                setError("");
                void onDismiss()
                  .catch((cause: unknown) =>
                    setError(
                      cause instanceof Error ? cause.message : "Could not dismiss this message.",
                    ),
                  )
                  .finally(() => setPending(false));
              }}
            >
              {pending ? "Dismissing…" : "Dismiss"}
            </Button>
          )}
        </div>
        {error && (
          <p role="alert" className="text-destructive text-sm">
            {error}
          </p>
        )}
      </div>
    </article>
  );
}
