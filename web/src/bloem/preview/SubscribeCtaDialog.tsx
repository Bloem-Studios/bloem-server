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
import { safePreviewHref, subscriptionHref } from "./routes";
import { previewEvent } from "./api";
export default function SubscribeCtaDialog({
  open,
  onOpenChange,
  item,
  ended = false,
}: {
  open: boolean;
  onOpenChange: (value: boolean) => void;
  item?: string;
  ended?: boolean;
}) {
  const { presentation, guest } = usePreviewMode();
  const { serverName } = useBranding();
  const href = subscriptionHref(
    presentation?.cta_url ?? "",
    item,
    guest ? guestSession()?.ref : undefined,
  );
  const launch = guest ? safePreviewHref(presentation?.launch_url) : null;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {ended ? "Your preview session has ended" : `Subscribe to ${serverName}`}
          </DialogTitle>
          <DialogDescription>
            {ended
              ? "Start your trial to enjoy the full library."
              : "Browse the library here. Subscribe to watch, read and listen."}
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-wrap gap-3">
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
