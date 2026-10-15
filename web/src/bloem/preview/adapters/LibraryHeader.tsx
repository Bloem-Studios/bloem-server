import type { ComponentProps } from "react";
import Original from "../../../components/LibraryHeader";
import { usePreviewMode } from "../usePreviewMode";
export default function LibraryHeader(props: ComponentProps<typeof Original>) {
  return <Original {...props} onShuffle={usePreviewMode().active ? undefined : props.onShuffle} />;
}
