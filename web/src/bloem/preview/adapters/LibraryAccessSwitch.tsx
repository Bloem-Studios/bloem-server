import type { ComponentProps } from "react";

import { useBloemT } from "@/bloem/i18n";
import { Switch as Original } from "@/components/ui/switch";

/**
 * LibraryAccessSelector's switches. A library's switch carries
 * `data-bloem-library` (the library's name) and is named "Allow <library>"
 * through Bloem's catalog. The all-libraries switch keeps the name Silo gives
 * it, such as "All libraries" or "Inherit from access group".
 */
export function Switch({
  "data-bloem-library": library,
  ...props
}: ComponentProps<typeof Original> & { "data-bloem-library"?: string }) {
  const bt = useBloemT();
  if (library === undefined) {
    return <Original {...props} />;
  }
  return <Original {...props} aria-label={bt("bloem.libraryAccess.allowLibrary", { library })} />;
}
