import { useState, type FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { AccountEntitlementPanel } from "./AccountEntitlementPanel";

export default function DirectAccountsPage() {
  useDocumentTitle("Direct Account Entitlements");
  const [accountInput, setAccountInput] = useState("");
  const [selectedAccountID, setSelectedAccountID] = useState<string | null>(null);
  const [error, setError] = useState("");

  function loadAccount(event: FormEvent) {
    event.preventDefault();
    const accountID = accountInput.trim();
    if (!/^[1-9]\d*$/.test(accountID)) {
      setError("Enter a positive numeric account ID.");
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
            Apply a versioned entitlement to an account managed directly by this Vondel instance.
          </p>
        </div>
      </div>
      <form
        className="surface-panel flex flex-col gap-3 rounded-2xl p-5 sm:flex-row sm:items-end"
        onSubmit={loadAccount}
      >
        <div className="flex-1 space-y-1.5">
          <Label htmlFor="direct-account-id">Account ID</Label>
          <Input
            id="direct-account-id"
            inputMode="numeric"
            value={accountInput}
            onChange={(event) => setAccountInput(event.target.value)}
          />
          {error ? (
            <p className="text-destructive text-sm" role="alert">
              {error}
            </p>
          ) : null}
        </div>
        <Button type="submit">Load account</Button>
      </form>
      {selectedAccountID ? <AccountEntitlementPanel userID={selectedAccountID} /> : null}
    </section>
  );
}
