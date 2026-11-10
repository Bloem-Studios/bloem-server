import { useState } from "react";
import { useBloemT } from "@/bloem/i18n";
import { Globe } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  useAddOrganizationDomain,
  useOrganizationDomains,
  useRemoveOrganizationDomain,
  useSetPrimaryOrganizationDomain,
} from "@/hooks/queries/admin/organizationDomains";

/** An example domain in the empty field: data, the same in every language. */
const EXAMPLE_HOST = "watch.example.com";

/**
 * An organization's domains (Bloem). Visitors on these domains see the
 * organization's branding, and its emails link to the primary one.
 */
export function OrganizationDomainsPanel({ organizationID }: { organizationID: string }) {
  const bt = useBloemT();
  const domains = useOrganizationDomains(organizationID);
  const add = useAddOrganizationDomain(organizationID);
  const setPrimary = useSetPrimaryOrganizationDomain(organizationID);
  const remove = useRemoveOrganizationDomain(organizationID);
  const [host, setHost] = useState("");
  const [primary, setPrimaryChoice] = useState(false);
  const [confirming, setConfirming] = useState<string | null>(null);
  const error = add.error ?? setPrimary.error ?? remove.error ?? domains.error;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Globe className="h-4 w-4" />
          {bt("bloem.admin.platformOrganizations.domains.title")}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <p className="text-muted-foreground text-sm">
          {bt("bloem.admin.platformOrganizations.domains.intro")}
        </p>
        {domains.isLoading ? (
          <p className="text-sm" role="status">
            {bt("bloem.admin.platformOrganizations.domains.loading")}
          </p>
        ) : (domains.data ?? []).length === 0 ? (
          <p className="text-muted-foreground text-sm">
            {bt("bloem.admin.platformOrganizations.domains.empty")}
          </p>
        ) : (
          <ul className="divide-border divide-y rounded-lg border">
            {(domains.data ?? []).map((domain) => (
              <li key={domain.host} className="flex flex-wrap items-center gap-2 p-3 text-sm">
                <span className="font-medium break-all">{domain.host}</span>
                {domain.primary ? (
                  <Badge>{bt("bloem.admin.platformOrganizations.domains.primary")}</Badge>
                ) : null}
                <span className="text-muted-foreground text-xs">
                  {domain.source === "garden"
                    ? bt("bloem.admin.platformOrganizations.domains.fromStorefront")
                    : bt("bloem.admin.platformOrganizations.domains.addedHere")}
                </span>
                <span className="ms-auto flex gap-2">
                  {!domain.primary ? (
                    <Button
                      size="sm"
                      variant="outline"
                      aria-label={bt("bloem.admin.platformOrganizations.domains.makePrimaryLabel", {
                        host: domain.host,
                      })}
                      disabled={setPrimary.isPending}
                      onClick={() => setPrimary.mutate(domain.host)}
                    >
                      {bt("bloem.admin.platformOrganizations.domains.makePrimary")}
                    </Button>
                  ) : null}
                  {confirming === domain.host ? (
                    <>
                      <Button
                        size="sm"
                        variant="destructive"
                        aria-label={bt(
                          "bloem.admin.platformOrganizations.domains.confirmRemoveLabel",
                          { host: domain.host },
                        )}
                        disabled={remove.isPending}
                        onClick={() =>
                          remove.mutate(domain.host, { onSettled: () => setConfirming(null) })
                        }
                      >
                        {bt("bloem.admin.platformOrganizations.domains.remove")}
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => setConfirming(null)}>
                        {bt("bloem.admin.platformOrganizations.domains.cancel")}
                      </Button>
                    </>
                  ) : (
                    <Button
                      size="sm"
                      variant="ghost"
                      aria-label={bt("bloem.admin.platformOrganizations.domains.removeLabel", {
                        host: domain.host,
                      })}
                      onClick={() => setConfirming(domain.host)}
                    >
                      {bt("bloem.admin.platformOrganizations.domains.remove")}
                    </Button>
                  )}
                </span>
              </li>
            ))}
          </ul>
        )}
        <form
          className="flex flex-wrap items-end gap-3"
          onSubmit={(event) => {
            event.preventDefault();
            const value = host.trim();
            if (!value) return;
            add.mutate(
              { host: value, primary },
              {
                onSuccess: () => {
                  setHost("");
                  setPrimaryChoice(false);
                },
              },
            );
          }}
        >
          <div className="grid gap-1">
            <Label htmlFor="organization-domain-host">
              {bt("bloem.admin.platformOrganizations.domains.domain")}
            </Label>
            <Input
              id="organization-domain-host"
              placeholder={EXAMPLE_HOST}
              value={host}
              onChange={(event) => setHost(event.target.value)}
            />
          </div>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={primary}
              onChange={(event) => setPrimaryChoice(event.target.checked)}
            />
            {bt("bloem.admin.platformOrganizations.domains.makeItPrimary")}
          </label>
          <Button type="submit" disabled={add.isPending || !host.trim()}>
            {bt("bloem.admin.platformOrganizations.domains.add")}
          </Button>
        </form>
        {error ? (
          <p className="text-destructive text-sm" role="alert">
            {error.message}
          </p>
        ) : null}
      </CardContent>
    </Card>
  );
}
