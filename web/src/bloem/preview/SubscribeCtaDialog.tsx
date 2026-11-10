import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { useBloemT } from "@/bloem/i18n";
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
  const bt = useBloemT();
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
    (minutes > 0
      ? bt("bloem.preview.dialog.playFreeFor", { minutes })
      : bt("bloem.preview.dialog.playFree"));
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {ended
              ? bt("bloem.preview.dialog.sessionEnded")
              : freePlayEnded
                ? bt("bloem.preview.dialog.freePlayEnded")
                : bt("bloem.preview.cta.subscribeToBrand", { brand: serverName })}
          </DialogTitle>
          <DialogDescription>
            {ended || freePlayEnded
              ? bt("bloem.preview.dialog.startTrialToEnjoy")
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
              <a href={launch}>{bt("bloem.preview.dialog.keepBrowsing")}</a>
            </Button>
          )}
          {href && (
            <Button asChild>
              <a href={href} onClick={() => previewEvent("cta_clicked")}>
                {ended
                  ? bt("bloem.preview.dialog.startTrial")
                  : presentation?.cta_label ||
                    bt("bloem.preview.cta.subscribeToBrand", { brand: serverName })}
              </a>
            </Button>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
