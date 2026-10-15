import type { ComponentProps } from "react";
import Original from "../../../components/CardPlayOverlay";
import { usePreviewMode } from "../usePreviewMode";
export default function CardPlayOverlay(props: ComponentProps<typeof Original>) {
  return usePreviewMode().active ? null : <Original {...props} />;
}
