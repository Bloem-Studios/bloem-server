// Bloem: account names for admin screens that only have account numbers.
import { useMemo } from "react";

import { useAdminUsers } from "@/hooks/queries/admin/users";

/** Looks up account names for display; falls back to the account number. */
export function useAccountNames(): (id: number) => string {
  const users = useAdminUsers();
  return useMemo(() => {
    const names = new Map((users.data ?? []).map((user) => [user.id, user.username]));
    return (id: number) => {
      const name = names.get(id);
      return name ? `${name} (account ${id})` : `Account ${id}`;
    };
  }, [users.data]);
}
