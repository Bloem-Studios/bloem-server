import type { ReactNode } from "react";
import Original from "../../../components/TasteSeedGate";
import { usePreviewMode } from "../usePreviewMode";
export default function TasteSeedGate({ children }: { children: ReactNode }) {
  return usePreviewMode().active ? children : <Original>{children}</Original>;
}
