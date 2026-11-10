import type { BloemMessageKey } from "@/bloem/i18n";
import type { MessageKey } from "@/i18n";
import { PERMISSION_WATCH_LIVE_TV } from "@/lib/bloemPermissions";

// Silo's list types its labels as Silo message ids. Bloem's are Bloem ids,
// which the access groups page formats through Bloem's i18n adapter
// (bloem-preview-plugin.ts).
const bloemLabel = (id: BloemMessageKey) => id as unknown as MessageKey;

/** Bloem's assignable permissions, listed before Silo's on the access groups page. */
export const BLOEM_GROUP_PERMISSIONS = [
  {
    value: PERMISSION_WATCH_LIVE_TV,
    label: bloemLabel("bloem.admin.accessGroups.liveTv.label"),
    description: bloemLabel("bloem.admin.accessGroups.liveTv.description"),
  },
];
