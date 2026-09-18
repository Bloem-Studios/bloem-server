import { useState } from "react";
import Home from "./Home";
import { Button } from "@/components/ui/button";
import { CampaignRail } from "@/components/engagement/CampaignPlacements";
import { useCampaigns, useHomeCampaignPreference } from "@/components/engagement/useCampaigns";

export default function BloemHome() {
  const preference = useHomeCampaignPreference();
  const campaigns = useCampaigns("home", "", preference.enabled);
  const [error, setError] = useState("");
  return (
    <Home
      renderSupplementalRow={(index, total) => (
        <>
          {index === Math.min(total, Math.max(0, campaigns.position)) && (
            <div className={campaigns.cards.length ? "page-shell" : undefined}>
              <CampaignRail cards={campaigns.cards} dismiss={campaigns.dismiss} />
            </div>
          )}
          {index === total && preference.eligible && (
            <div className="page-shell space-y-2">
              <Button
                variant="ghost"
                size="sm"
                aria-pressed={preference.enabled}
                onClick={() => {
                  const persisted = preference.setEnabled(!preference.enabled);
                  setError(
                    persisted
                      ? ""
                      : "Browser storage is unavailable. Your choice applies in this tab only.",
                  );
                }}
              >
                Home promotions: {preference.enabled ? "On" : "Off"}
              </Button>
              <p className="text-muted-foreground text-xs">
                Optional messages from your server, saved for this profile in this browser.
              </p>
              {error && (
                <p role="alert" className="text-destructive text-sm">
                  {error}
                </p>
              )}
            </div>
          )}
        </>
      )}
    />
  );
}
