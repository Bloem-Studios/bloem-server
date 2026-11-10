// Bloem: find server accounts by name or email instead of typing account numbers.
import { useBloemT } from "@/bloem/i18n";
import { Search, X } from "lucide-react";
import { useMemo, useState } from "react";

import type { AdminUser } from "@/api/types";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAdminUsers } from "@/hooks/queries/admin/users";

const MAX_RESULTS = 8;

export function AccountPicker({
  id,
  label,
  selected,
  onChange,
  multiple = true,
}: {
  id: string;
  label: string;
  selected: number[];
  onChange(ids: number[]): void;
  multiple?: boolean;
}) {
  const bt = useBloemT();
  const users = useAdminUsers();
  const [query, setQuery] = useState("");
  const byID = useMemo(
    () => new Map((users.data ?? []).map((user) => [user.id, user])),
    [users.data],
  );
  const matches = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return [];
    return (users.data ?? [])
      .filter(
        (user) =>
          !selected.includes(user.id) &&
          (user.username.toLowerCase().includes(needle) ||
            user.email.toLowerCase().includes(needle) ||
            String(user.id) === needle),
      )
      .slice(0, MAX_RESULTS);
  }, [query, selected, users.data]);

  function pick(user: AdminUser) {
    onChange(multiple ? [...selected, user.id] : [user.id]);
    setQuery("");
  }

  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      <div className="relative">
        <Search
          className="text-muted-foreground pointer-events-none absolute start-3 top-1/2 size-4 -translate-y-1/2"
          aria-hidden
        />
        <Input
          id={id}
          className="ps-9"
          placeholder={bt("bloem.admin.policy.accountPicker.nameOrEmail")}
          autoComplete="off"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && matches[0]) {
              event.preventDefault();
              pick(matches[0]);
            }
          }}
        />
      </div>
      {users.isError ? (
        <p className="text-muted-foreground text-sm">
          {bt("bloem.admin.policy.accountPicker.accountSearchIsUnavailableEnter")}
        </p>
      ) : query.trim() ? (
        matches.length ? (
          <ul className="border-border divide-border divide-y rounded-lg border" role="listbox">
            {matches.map((user) => (
              <li key={user.id} role="option" aria-selected={false}>
                <button
                  type="button"
                  className="hover:bg-muted/50 flex w-full items-center justify-between gap-3 px-3 py-2 text-start text-sm transition-colors"
                  onClick={() => pick(user)}
                >
                  <span className="min-w-0">
                    <span className="block truncate font-medium">{user.username}</span>
                    <span className="text-muted-foreground block truncate text-xs">
                      {user.email}
                    </span>
                  </span>
                  <span className="text-muted-foreground shrink-0 font-mono text-xs">
                    {user.id}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-muted-foreground text-sm">
            {bt("bloem.admin.policy.accountPicker.noAccountsMatch")}
          </p>
        )
      ) : null}
      {multiple && selected.length ? (
        <ul
          className="flex flex-wrap gap-2"
          aria-label={bt("bloem.admin.policy.accountPicker.chosenAccounts")}
        >
          {selected.map((accountID) => {
            const user = byID.get(accountID);
            return (
              <li
                key={accountID}
                className="border-border bg-muted/40 flex items-center gap-1 rounded-md border py-1 ps-2 pe-1 text-sm"
              >
                {user
                  ? user.username
                  : bt("bloem.admin.policy.accountPicker.account", { accountID })}
                <button
                  type="button"
                  className="text-muted-foreground hover:text-foreground hover:bg-muted rounded p-0.5 transition-colors"
                  aria-label={bt("bloem.admin.policy.accountPicker.remove", {
                    account: user
                      ? user.username
                      : bt("bloem.admin.policy.accountPicker.accountLower", { accountID }),
                  })}
                  onClick={() => onChange(selected.filter((value) => value !== accountID))}
                >
                  <X className="size-3.5" aria-hidden />
                </button>
              </li>
            );
          })}
        </ul>
      ) : null}
    </div>
  );
}
