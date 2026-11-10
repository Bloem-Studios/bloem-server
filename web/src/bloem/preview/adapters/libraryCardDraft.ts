import { LIVE_TV_ROW } from "@/bloem/admin/LiveTvPermissionRows";
import { PERMISSION_WATCH_LIVE_TV } from "@/lib/bloemPermissions";
import { hasAssignedPermission, setAssignedPermission } from "@/lib/permissions";
import {
  rebaseDraft,
  useAccountCardDraft as useSiloAccountCardDraft,
} from "@/pages/admin-users/detail/access/useAccountCardDraft";

export * from "@/pages/admin-users/detail/access/useAccountCardDraft";

type Options<D, R extends string> = Parameters<typeof useSiloAccountCardDraft<D, R>>[0];

function permissionsOf(draft: unknown): string[] {
  return (draft as { permissions?: string[] }).permissions ?? [];
}

/**
 * The library card's draft, with Bloem's Live TV permission beside Silo's
 * permissions: a change to it counts as a changed row, and a reload keeps
 * the admin's choice the way Silo's rebase keeps theirs.
 */
export function useAccountCardDraft<D, R extends string = string>(opts: Options<D, R>) {
  const rebase = opts.rebase ?? rebaseDraft;
  const library: Options<D, R> = {
    ...opts,
    changedRows: (draft, base) => {
      const rows = opts.changedRows(draft, base);
      const mine = hasAssignedPermission(permissionsOf(draft), PERMISSION_WATCH_LIVE_TV);
      const theirs = hasAssignedPermission(permissionsOf(base), PERMISSION_WATCH_LIVE_TV);
      return mine === theirs ? rows : [...rows, LIVE_TV_ROW as R];
    },
    rebase: (draft, oldBase, freshBase) => {
      const rebased = rebase(draft, oldBase, freshBase);
      const mine = hasAssignedPermission(permissionsOf(draft), PERMISSION_WATCH_LIVE_TV);
      if (mine === hasAssignedPermission(permissionsOf(oldBase), PERMISSION_WATCH_LIVE_TV)) {
        return rebased;
      }
      const permissions = setAssignedPermission(
        permissionsOf(rebased),
        PERMISSION_WATCH_LIVE_TV,
        mine,
      );
      return { ...rebased, permissions: [...permissions].sort() };
    },
  };
  return useSiloAccountCardDraft(opts.id === "library" ? library : opts);
}
