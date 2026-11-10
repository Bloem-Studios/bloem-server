import { useBloemT } from "@/bloem/i18n";
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
  const bt = useBloemT();
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
          <DialogTitle>
            {bt("bloem.admin.organizations.ownershipTransfer.transferOwnership")}
          </DialogTitle>
          <DialogDescription>
            {bt("bloem.admin.organizations.ownershipTransfer.theOwnerCanTBe", {
              name: organization.name,
            })}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <AccountPicker
              id="new-owner-search"
              label={bt("bloem.admin.organizations.ownershipTransfer.findTheNewOwner")}
              multiple={false}
              selected={/^[1-9]\d*$/.test(ownerAccountId) ? [Number(ownerAccountId)] : []}
              onChange={(ids) => setOwnerAccountId(ids[0] ? String(ids[0]) : "")}
            />
            <Label htmlFor="new-owner-account" className="text-muted-foreground text-xs">
              {bt("bloem.admin.organizations.ownershipTransfer.newOwner")}
            </Label>
            <Input
              className="sm:max-w-40"
              id="new-owner-account"
              inputMode="numeric"
              value={ownerAccountId}
              onChange={(event) => setOwnerAccountId(event.target.value)}
              placeholder={bt("bloem.admin.organizations.ownershipTransfer.accountNumber")}
            />
            {ownerError ? <p className="text-destructive text-sm">{ownerError}</p> : null}
          </div>
          <div className="space-y-2">
            <Label htmlFor="ownership-confirmation">
              {bt("bloem.admin.organizations.ownershipTransfer.typeToConfirm", {
                name: organization.name,
              })}
            </Label>
            <Input
              id="ownership-confirmation"
              value={confirmation}
              onChange={(event) => setConfirmation(event.target.value)}
              autoComplete="off"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="ownership-password">
              {bt("bloem.admin.organizations.ownershipTransfer.yourPassword")}
            </Label>
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
            {bt("bloem.admin.organizations.ownershipTransfer.cancel")}
          </Button>
          <Button
            variant="destructive"
            disabled={!canSubmit}
            onClick={() => onTransfer({ ownerAccountId: parsedOwner, password })}
          >
            {pending
              ? bt("bloem.admin.organizations.ownershipTransfer.transferring")
              : bt("bloem.admin.organizations.ownershipTransfer.transferOwnership")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
