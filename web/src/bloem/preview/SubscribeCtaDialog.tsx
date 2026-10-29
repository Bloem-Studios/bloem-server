import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { useBranding } from "@/hooks/useBranding";
import { usePreviewMode } from "./usePreviewMode";
import { guestSession } from "./session";
import { freePlayHref, safePreviewHref, subscriptionHref } from "./routes";
import { previewEvent } from "./api";
import { ctaMessage } from "./wording";
export default function SubscribeCtaDialog({
  open,
  onOpenChange,
  item,
  ended = false,
  freePlayEnded = false,
}: {
  open: boolean;
  onOpenChange: (value: boolean) => void;
  item?: string;
  ended?: boolean;
  freePlayEnded?: boolean;
}) {
  const { presentation, guest, freePlay } = usePreviewMode();
  const { serverName } = useBranding();
  const href = subscriptionHref(
    presentation?.cta_url ?? "",
    item,
    guest ? guestSession()?.ref : undefined,
  );
  const launch = guest ? safePreviewHref(presentation?.launch_url) : null;
  // A browsing guest may ask the storefront for free play, once.
  const free =
    guest && !ended && !freePlay && !freePlayEnded && !guestSession()?.free_play_ended
      ? freePlayHref(presentation?.free_play_url, item, guestSession()?.ref)
      : null;
  const minutes = Math.round((presentation?.free_play_seconds ?? 0) / 60);
  const freeLabel =
    presentation?.free_play_label ||
    (minutes > 0 ? `Play free for ${minutes} minutes` : "Play free");
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {ended
              ? "Your preview session has ended"
              : freePlayEnded
                ? "Your free play has ended"
                : `Subscribe to ${serverName}`}
          </DialogTitle>
          <DialogDescription>
            {ended || freePlayEnded
              ? "Start your trial to enjoy the full library."
              : ctaMessage(presentation)}
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-wrap gap-3">
          {free && (
            <Button variant="outline" asChild>
              <a href={free}>{freeLabel}</a>
            </Button>
          )}
          {ended && launch && (
            <Button variant="outline" asChild>
              <a href={launch}>Keep browsing</a>
            </Button>
          )}
          {href && (
            <Button asChild>
              <a href={href} onClick={() => previewEvent("cta_clicked")}>
                {ended
                  ? "Start your trial"
                  : presentation?.cta_label || `Subscribe to ${serverName}`}
              </a>
            </Button>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
