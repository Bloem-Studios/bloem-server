import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { useBloemT } from "@/bloem/i18n";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { AccountPicker } from "@/components/admin/policy/AccountPicker";
import { AccountEntitlementPanel } from "./AccountEntitlementPanel";

export default function DirectAccountsPage() {
  const bt = useBloemT();
  useDocumentTitle(bt("bloem.admin.directAccounts.title"));
  const [accountInput, setAccountInput] = useState("");
  const [selectedAccountID, setSelectedAccountID] = useState<string | null>(null);
  const [invalidNumber, setInvalidNumber] = useState(false);

  function loadAccount(event: FormEvent) {
    event.preventDefault();
    const accountID = accountInput.trim();
    if (!/^[1-9]\d*$/.test(accountID)) {
      setInvalidNumber(true);
      return;
    }
    setInvalidNumber(false);
    setSelectedAccountID(accountID);
  }

  return (
    <section className="page-shell space-y-6 py-4 sm:py-6">
      <div className="page-header">
        <div>
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]">
            {bt("bloem.admin.directAccounts.title")}
          </h1>
          <p className="page-subtitle text-sm sm:text-base">
            {bt("bloem.admin.directAccounts.intro")}
          </p>
        </div>
        <Button asChild variant="outline">
          <Link to="/admin/platform/direct-accounts/bulk">
            {bt("bloem.admin.directAccounts.changeManyAccounts")}
          </Link>
        </Button>
      </div>
      <section className="surface-panel space-y-4 rounded-2xl p-5">
        <AccountPicker
          id="direct-account-search"
          label={bt("bloem.admin.directAccounts.findAnAccount")}
          multiple={false}
          selected={selectedAccountID ? [Number(selectedAccountID)] : []}
          onChange={(ids) => {
            setInvalidNumber(false);
            setSelectedAccountID(ids[0] ? String(ids[0]) : null);
          }}
        />
        <form className="flex flex-col gap-3 sm:flex-row sm:items-end" onSubmit={loadAccount}>
          <div className="flex-1 space-y-1.5">
            <Label htmlFor="direct-account-id">
              {bt("bloem.admin.directAccounts.orEnterAnAccountNumber")}
            </Label>
            <Input
              id="direct-account-id"
              inputMode="numeric"
              className="sm:max-w-48"
              value={accountInput}
              onChange={(event) => setAccountInput(event.target.value)}
            />
            {invalidNumber ? (
              <p className="text-destructive text-sm" role="alert">
                {bt("bloem.admin.directAccounts.accountNumberNotWhole")}
              </p>
            ) : null}
          </div>
          <Button type="submit" variant="outline">
            {bt("bloem.admin.directAccounts.openAccount")}
          </Button>
        </form>
      </section>
      {selectedAccountID ? <AccountEntitlementPanel userID={selectedAccountID} /> : null}
    </section>
  );
}
