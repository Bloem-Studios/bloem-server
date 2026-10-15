import type { ComponentType } from "react";
import { useAdminContext } from "@/contexts/AdminContextProvider";

export function withBloemPlatformWorkspace(Workspace:ComponentType) {
 return function PlatformWorkspace() {
  const {active}=useAdminContext();
  if(active?.scope!=="platform") return <div role="alert">This workspace is available only in Platform context.</div>;
  return <Workspace/>;
 };
}
