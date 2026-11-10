import type { ComponentProps } from "react";

import Original from "@/components/ServerActivity";
import { useOptionalAdminContext } from "@/contexts/AdminContextProvider";

/** The admin header's server activity belongs to the platform: organization administrators don't see it. */
export default function ServerActivity(props: ComponentProps<typeof Original>) {
  const platformScope = useOptionalAdminContext()?.active?.scope === "platform";
  return platformScope ? <Original {...props} /> : null;
}
