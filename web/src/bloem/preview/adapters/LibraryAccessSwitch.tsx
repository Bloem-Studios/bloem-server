import type { ComponentProps } from "react";

import { useBloemT } from "@/bloem/i18n";
import { useLibraryAccessScope } from "@/bloem/libraryAccessScope";
import { Switch as Original } from "@/components/ui/switch";

/**
 * LibraryAccessSelector's switches. Silo's all-libraries switch has no
 * accessible name of its own; it gets one naming whose libraries it allows.
 * A switch that names itself (each library's) keeps its own.
 */
export function Switch(props: ComponentProps<typeof Original>) {
  const bt = useBloemT();
  const scope = useLibraryAccessScope();
  const name = scope
    ? bt("bloem.libraryAccess.allowAllIn", { scope })
    : bt("bloem.libraryAccess.allowAll");
  return <Original aria-label={name} {...props} />;
}
