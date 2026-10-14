import type { ComponentProps } from "react";
import Original from "../../../pages/ItemDetail/AudiobookContent";
import { usePreviewMode } from "../usePreviewMode";
import PreviewBookDetail from "../PreviewBookDetail";
export default function AudiobookContent(props: ComponentProps<typeof Original>) {
  return usePreviewMode().active ? <PreviewBookDetail {...props} /> : <Original {...props} />;
}
