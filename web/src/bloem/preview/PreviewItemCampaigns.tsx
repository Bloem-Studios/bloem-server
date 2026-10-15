import type { ReactNode } from "react";
import { ItemCampaigns } from "@/components/engagement/CampaignPlacements";
import { usePreviewMode } from "./usePreviewMode";
export default function PreviewItemCampaigns({ children }: { children: ReactNode }) {
  return usePreviewMode().active ? children : <ItemCampaigns>{children}</ItemCampaigns>;
}
