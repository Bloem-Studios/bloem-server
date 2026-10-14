import type { ReactNode } from "react";
import { OnboardingGate as Original } from "../../../components/onboarding/OnboardingGate";
import { usePreviewMode } from "../usePreviewMode";
export function OnboardingGate({ children }: { children: ReactNode }) {
  return usePreviewMode().active ? children : <Original>{children}</Original>;
}
