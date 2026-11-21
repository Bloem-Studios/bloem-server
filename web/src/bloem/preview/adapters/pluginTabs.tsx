import type { ComponentProps } from "react";
import { useSearchParams } from "react-router";

import { useOptionalAdminContext } from "@/contexts/AdminContextProvider";
import { Tabs as SiloTabs } from "@/components/ui/tabs";

export { TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

/**
 * The plugins page's tabs with Bloem's storage tab. Inside an administrative
 * context, ?tab=storage selects the storage tab, and choosing it puts it in the
 * URL; Silo's page handles its own tabs as before.
 */
export function Tabs({ value, onValueChange, ...props }: ComponentProps<typeof SiloTabs>) {
  const [searchParams, setSearchParams] = useSearchParams();
  // The page reads the same context (AdminPlugins.tsx).
  const storageAvailable = (useOptionalAdminContext()?.active ?? null) !== null;
  const storage = storageAvailable && searchParams.get("tab") === "storage";
  return (
    <SiloTabs
      {...props}
      value={storage ? "storage" : value}
      onValueChange={(next) => {
        if (next !== "storage") {
          onValueChange?.(next);
          return;
        }
        const params = new URLSearchParams(searchParams);
        params.set("tab", "storage");
        setSearchParams(params, { replace: false });
      }}
    />
  );
}
