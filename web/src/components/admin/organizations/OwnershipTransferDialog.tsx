import { useState } from "react";
import { AccountPicker } from "@/components/admin/policy/AccountPicker";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { PlatformOrganization } from "@/hooks/queries/admin/organizations";

interface OwnershipTransferDialogProps {
  organization: PlatformOrganization;
  open: boolean;
  onOpenChange(open: boolean): void;
  pending?: boolean;
  error?: string | null;
  ownerError?: string | null;
  onTransfer(input: { ownerAccountId: number; password: string }): void;
}

export function OwnershipTransferDialog({
  organization,
  open,
  onOpenChange,
  pending = false,
  error,
  ownerError,
  onTransfer,
}: OwnershipTransferDialogProps) {
  const [ownerAccountId, setOwnerAccountId] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [password, setPassword] = useState("");

  function changeOpen(nextOpen: boolean) {
    if (!nextOpen) {
      setOwnerAccountId("");
      setConfirmation("");
      setPassword("");
    }
    onOpenChange(nextOpen);
  }

  const parsedOwner = Number(ownerAccountId);
  const canSubmit =
    Number.isInteger(parsedOwner) &&
    parsedOwner > 0 &&
    parsedOwner !== organization.owner_account_id &&
    confirmation === organization.name &&
    password.length > 0 &&
    !pending;

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Transfer ownership</DialogTitle>
          <DialogDescription>
            The owner can’t be removed from {organization.name}. The new owner must already be an
            active member.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <AccountPicker
              id="new-owner-search"
              label="Find the new owner"
              multiple={false}
              selected={/^[1-9]\d*$/.test(ownerAccountId) ? [Number(ownerAccountId)] : []}
              onChange={(ids) => setOwnerAccountId(ids[0] ? String(ids[0]) : "")}
            />
            <Label htmlFor="new-owner-account" className="text-muted-foreground text-xs">
              New owner
            </Label>
            <Input
              className="sm:max-w-40"
              id="new-owner-account"
              inputMode="numeric"
              value={ownerAccountId}
              onChange={(event) => setOwnerAccountId(event.target.value)}
              placeholder="Account number"
            />
            {ownerError ? <p className="text-destructive text-sm">{ownerError}</p> : null}
          </div>
          <div className="space-y-2">
            <Label htmlFor="ownership-confirmation">Type {organization.name} to confirm</Label>
            <Input
              id="ownership-confirmation"
              value={confirmation}
              onChange={(event) => setConfirmation(event.target.value)}
              autoComplete="off"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="ownership-password">Your password</Label>
            <Input
              id="ownership-password"
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              autoComplete="current-password"
            />
          </div>
          {error ? (
            <p className="text-destructive text-sm" role="alert">
              {error}
            </p>
          ) : null}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => changeOpen(false)} disabled={pending}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            disabled={!canSubmit}
            onClick={() => onTransfer({ ownerAccountId: parsedOwner, password })}
          >
            {pending ? "Transferring…" : "Transfer ownership"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
