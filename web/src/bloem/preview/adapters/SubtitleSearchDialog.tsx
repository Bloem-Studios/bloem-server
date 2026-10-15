import type { ComponentProps } from "react";
import Original from "../../../pages/ItemDetail/components/SubtitleSearchDialog";
import { usePreviewMode } from "../usePreviewMode";
export default function SubtitleSearchDialog(props: ComponentProps<typeof Original>) {
  return usePreviewMode().active ? null : <Original {...props} />;
}
