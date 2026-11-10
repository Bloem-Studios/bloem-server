import { useBloemT } from "@/bloem/i18n";
import { usePreviewMode } from "@/bloem/preview/usePreviewMode";
import { useState } from "react";
import Home from "./Home";
import { Button } from "@/components/ui/button";
import { CampaignRail } from "@/components/engagement/CampaignPlacements";
import { useCampaigns, useHomeCampaignPreference } from "@/components/engagement/useCampaigns";

export default function BloemHome() {
  const bt = useBloemT();
  const preview = usePreviewMode().active;
  const preference = useHomeCampaignPreference();
  const campaigns = useCampaigns("home", "", !preview && preference.enabled);
  const [storageUnavailable, setStorageUnavailable] = useState(false);
  return (
    <Home
      renderSupplementalRow={(index, total) => (
        <>
          {index === Math.min(total, Math.max(0, campaigns.position)) && (
            <div className={campaigns.cards.length ? "page-shell" : undefined}>
              <CampaignRail cards={campaigns.cards} dismiss={campaigns.dismiss} />
            </div>
          )}
          {index === total && !preview && preference.eligible && (
            <div className="page-shell space-y-2">
              <Button
                variant="ghost"
                size="sm"
                aria-pressed={preference.enabled}
                onClick={() => {
                  setStorageUnavailable(!preference.setEnabled(!preference.enabled));
                }}
              >
                {bt("bloem.engagement.home.toggle", { state: preference.enabled ? "on" : "off" })}
              </Button>
              <p className="text-muted-foreground text-xs">
                {bt("bloem.engagement.home.toggleHelp")}
              </p>
              {storageUnavailable && (
                <p role="alert" className="text-destructive text-sm">
                  {bt("bloem.engagement.home.storageUnavailable")}
                </p>
              )}
            </div>
          )}
        </>
      )}
    />
  );
}
