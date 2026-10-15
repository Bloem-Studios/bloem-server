import type { ReactNode } from "react";
import { BrandingProvider as Original } from "@/contexts/BrandingProvider";
import { PreviewShell } from "./PreviewShell";

/** Compose preview chrome here so the shared app keeps its provider tree. */
export function BrandingProvider({ children }: { children: ReactNode }) {
  return (
    <Original>
      <PreviewShell>{children}</PreviewShell>
    </Original>
  );
}
