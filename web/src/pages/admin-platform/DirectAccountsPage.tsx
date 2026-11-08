import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { AccountPicker } from "@/components/admin/policy/AccountPicker";
import { AccountEntitlementPanel } from "./AccountEntitlementPanel";

export default function DirectAccountsPage() {
  useDocumentTitle("Direct Accounts");
  const [accountInput, setAccountInput] = useState("");
  const [selectedAccountID, setSelectedAccountID] = useState<string | null>(null);
  const [error, setError] = useState("");

  function loadAccount(event: FormEvent) {
    event.preventDefault();
    const accountID = accountInput.trim();
    if (!/^[1-9]\d*$/.test(accountID)) {
      setError("Account numbers are whole numbers, like 41.");
      return;
    }
    setError("");
    setSelectedAccountID(accountID);
  }

  return (
    <section className="page-shell space-y-6 py-4 sm:py-6">
      <div className="page-header">
        <div>
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]">Direct Accounts</h1>
          <p className="page-subtitle text-sm sm:text-base">
            Accounts that sign up straight to this server, not through an organization. Find one to
            see or change its plan.
          </p>
        </div>
        <Button asChild variant="outline">
          <Link to="/admin/platform/direct-accounts/bulk">Change many accounts</Link>
        </Button>
      </div>
      <section className="surface-panel space-y-4 rounded-2xl p-5">
        <AccountPicker
          id="direct-account-search"
          label="Find an account"
          multiple={false}
          selected={selectedAccountID ? [Number(selectedAccountID)] : []}
          onChange={(ids) => {
            setError("");
            setSelectedAccountID(ids[0] ? String(ids[0]) : null);
          }}
        />
        <form className="flex flex-col gap-3 sm:flex-row sm:items-end" onSubmit={loadAccount}>
          <div className="flex-1 space-y-1.5">
            <Label htmlFor="direct-account-id">Or enter an account number</Label>
            <Input
              id="direct-account-id"
              inputMode="numeric"
              className="sm:max-w-48"
              value={accountInput}
              onChange={(event) => setAccountInput(event.target.value)}
            />
            {error ? (
              <p className="text-destructive text-sm" role="alert">
                {error}
              </p>
            ) : null}
          </div>
          <Button type="submit" variant="outline">
            Open account
          </Button>
        </form>
      </section>
      {selectedAccountID ? <AccountEntitlementPanel userID={selectedAccountID} /> : null}
    </section>
  );
}
