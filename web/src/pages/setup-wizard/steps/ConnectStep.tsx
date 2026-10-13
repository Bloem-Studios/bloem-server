import { StepFrame, StepSection } from "../StepFrame";
import { useStepSubmit, useStepSummary } from "../useStep";
import { SILO_APP_EXAMPLES } from "@/pages/settings/connectApps";

const nativeForm = { dirtyCount: 0, isSaving: false, save: async () => {} };

export function ConnectStep() {
  const { handleSubmit, busy, skip } = useStepSubmit("connect", nativeForm, "Failed to continue");
  useStepSummary("connect", "Native apps");
  return (
    <StepFrame
      title="Connect apps"
      lede="Use the native apps or this website to browse and play your media."
      onSubmit={handleSubmit}
      busy={busy}
      onSkip={skip}
    >
      <StepSection title="Native apps" caption={SILO_APP_EXAMPLES}>
        <p className="text-muted-foreground text-sm">
          Connect to this server's address and use the same sign-in method as the website. Household
          profiles and PINs are selected after sign-in.
        </p>
      </StepSection>
    </StepFrame>
  );
}
