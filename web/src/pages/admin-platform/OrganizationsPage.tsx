import { useEffect, useState } from "react";
import { useBloemT } from "@/bloem/i18n";
import { AdminV2ClientError } from "@/api/adminV2Client";
import { Building2, ChevronLeft, ChevronRight, Plus, Search } from "lucide-react";
import { Link, useNavigate } from "react-router";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
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
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  useCreateOrganization,
  usePlatformOrganizations,
  type OrganizationStatus,
} from "@/hooks/queries/admin/organizations";
import { AccountPicker } from "@/components/admin/policy/AccountPicker";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

const slugPattern = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

function CreateOrganizationDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange(open: boolean): void;
}) {
  const bt = useBloemT();
  const navigate = useNavigate();
  const mutation = useCreateOrganization();
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [owner, setOwner] = useState("");
  const [slugEdited, setSlugEdited] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});

  function close(openState: boolean) {
    onOpenChange(openState);
    if (!openState) {
      setName("");
      setSlug("");
      setOwner("");
      setSlugEdited(false);
      setErrors({});
      mutation.reset();
    }
  }

  async function submit() {
    const nextErrors: Record<string, string> = {};
    if (!name.trim()) nextErrors.name = bt("bloem.admin.platformOrganizations.create.nameRequired");
    if (!slugPattern.test(slug)) {
      nextErrors.slug = bt("bloem.admin.platformOrganizations.create.slugInvalid");
    }
    const ownerId = Number(owner);
    if (!Number.isInteger(ownerId) || ownerId <= 0)
      nextErrors.owner = bt("bloem.admin.platformOrganizations.create.ownerRequired");
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;
    try {
      const result = await mutation.mutateAsync({
        name: name.trim(),
        slug,
        owner_account_id: ownerId,
      });
      close(false);
      navigate(`/admin/platform/organizations/${result.organization.id}`);
    } catch (error) {
      if (error instanceof AdminV2ClientError) {
        setErrors({
          name: error.fields.name ?? "",
          slug: error.fields.slug ?? "",
          owner: error.fields.owner_account_id ?? "",
        });
      }
    }
  }

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{bt("bloem.admin.platformOrganizations.create.title")}</DialogTitle>
          <DialogDescription>
            {bt("bloem.admin.platformOrganizations.create.description")}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="new-organization-name">
              {bt("bloem.admin.platformOrganizations.create.name")}
            </Label>
            <Input
              id="new-organization-name"
              value={name}
              onChange={(e) => {
                setName(e.target.value);
                if (!slugEdited) setSlug(slugify(e.target.value));
              }}
            />
            {errors.name ? <p className="text-destructive text-sm">{errors.name}</p> : null}
          </div>
          <div className="space-y-2">
            <Label htmlFor="new-organization-slug">
              {bt("bloem.admin.platformOrganizations.create.shortName")}
            </Label>
            <Input
              id="new-organization-slug"
              className="font-mono"
              value={slug}
              onChange={(e) => {
                setSlug(e.target.value);
                setSlugEdited(true);
              }}
            />
            <p className="text-muted-foreground text-xs">
              {bt("bloem.admin.platformOrganizations.create.shortNameHelp")}
            </p>
            {errors.slug ? <p className="text-destructive text-sm">{errors.slug}</p> : null}
          </div>
          <div className="space-y-2">
            <AccountPicker
              id="new-organization-owner-search"
              label={bt("bloem.admin.platformOrganizations.create.owner")}
              multiple={false}
              selected={/^[1-9]\d*$/.test(owner) ? [Number(owner)] : []}
              onChange={(ids) => setOwner(ids[0] ? String(ids[0]) : "")}
            />
            <Label htmlFor="new-organization-owner" className="text-muted-foreground text-xs">
              {bt("bloem.admin.platformOrganizations.create.ownerAccountNumber")}
            </Label>
            <Input
              id="new-organization-owner"
              inputMode="numeric"
              className="sm:max-w-40"
              value={owner}
              onChange={(e) => setOwner(e.target.value)}
            />
            {errors.owner ? <p className="text-destructive text-sm">{errors.owner}</p> : null}
          </div>
          {mutation.error ? (
            <p className="text-destructive text-sm" role="alert">
              {mutation.error.message}
            </p>
          ) : null}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => close(false)}>
            {bt("bloem.admin.platformOrganizations.create.cancel")}
          </Button>
          <Button onClick={() => void submit()} disabled={mutation.isPending}>
            {mutation.isPending
              ? bt("bloem.admin.platformOrganizations.create.creating")
              : bt("bloem.admin.platformOrganizations.create.create")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export default function OrganizationsPage() {
  const bt = useBloemT();
  useDocumentTitle(bt("bloem.adminTitle.organizations"));
  const [input, setInput] = useState("");
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState<OrganizationStatus | "all">("all");
  const [cursorHistory, setCursorHistory] = useState<string[]>([""]);
  const [createOpen, setCreateOpen] = useState(false);
  const cursor = cursorHistory[cursorHistory.length - 1] ?? "";

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setQuery(input.trim());
      setCursorHistory([""]);
    }, 250);
    return () => window.clearTimeout(timer);
  }, [input]);

  const organizations = usePlatformOrganizations({ query, status, cursor, limit: 50 });

  return (
    <section className="admin-page space-y-6">
      <div className="page-header">
        <div className="space-y-2">
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
            {bt("bloem.admin.platformOrganizations.list.title")}
          </h1>
          <p className="page-subtitle text-sm sm:text-base">
            {bt("bloem.admin.platformOrganizations.list.subtitle")}
          </p>
        </div>
        <Button onClick={() => setCreateOpen(true)}>
          <Plus className="me-2 h-4 w-4" />
          {bt("bloem.admin.platformOrganizations.list.create")}
        </Button>
      </div>

      <div className="surface-panel grid gap-3 rounded-2xl p-4 sm:grid-cols-[minmax(0,1fr)_12rem]">
        <div className="relative">
          <Search className="text-muted-foreground absolute start-3 top-1/2 h-4 w-4 -translate-y-1/2" />
          <Input
            type="search"
            aria-label={bt("bloem.admin.platformOrganizations.list.search")}
            placeholder={bt("bloem.admin.platformOrganizations.list.searchPlaceholder")}
            value={input}
            onChange={(event) => setInput(event.target.value)}
            className="ps-9"
          />
        </div>
        <select
          aria-label={bt("bloem.admin.platformOrganizations.list.statusFilter")}
          className="border-input bg-background h-9 rounded-md border px-3 text-sm"
          value={status}
          onChange={(event) => {
            setStatus(event.target.value as OrganizationStatus | "all");
            setCursorHistory([""]);
          }}
        >
          <option value="all">{bt("bloem.admin.platformOrganizations.list.anyStatus")}</option>
          <option value="active">{bt("bloem.admin.platformOrganizations.list.active")}</option>
          <option value="suspended">
            {bt("bloem.admin.platformOrganizations.list.suspended")}
          </option>
          <option value="initializing">
            {bt("bloem.admin.platformOrganizations.list.settingUp")}
          </option>
        </select>
      </div>

      {organizations.isLoading ? (
        <div
          className="space-y-3"
          role="status"
          aria-label={bt("bloem.admin.platformOrganizations.list.loading")}
        >
          {Array.from({ length: 5 }, (_, index) => (
            <Skeleton key={index} className="h-14 w-full" />
          ))}
        </div>
      ) : organizations.isError ? (
        <div
          className="border-destructive/30 bg-destructive/10 text-destructive rounded-xl border p-4"
          role="alert"
        >
          {organizations.error.message}
        </div>
      ) : (organizations.data?.organizations.length ?? 0) === 0 ? (
        <div className="surface-panel text-muted-foreground rounded-2xl p-10 text-center">
          <Building2 className="mx-auto mb-3 h-8 w-8" />
          {bt("bloem.admin.platformOrganizations.list.empty")}
        </div>
      ) : (
        <div className="surface-panel overflow-x-auto rounded-2xl">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{bt("bloem.admin.platformOrganizations.list.organization")}</TableHead>
                <TableHead>{bt("bloem.admin.platformOrganizations.list.status")}</TableHead>
                <TableHead>{bt("bloem.admin.platformOrganizations.list.people")}</TableHead>
                <TableHead>{bt("bloem.admin.platformOrganizations.list.profiles")}</TableHead>
                <TableHead>{bt("bloem.admin.platformOrganizations.list.libraries")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {organizations.data?.organizations.map((organization) => (
                <TableRow key={organization.id}>
                  <TableCell>
                    <Link
                      className="font-medium hover:underline"
                      to={`/admin/platform/organizations/${organization.id}`}
                    >
                      {organization.name}
                      <span className="text-muted-foreground block text-xs">
                        {organization.slug}
                      </span>
                    </Link>
                  </TableCell>
                  <TableCell>
                    <Badge variant={organization.status === "active" ? "default" : "secondary"}>
                      {bt("bloem.admin.platformOrganizations.status.organization", {
                        status: organization.status,
                      })}
                    </Badge>
                  </TableCell>
                  <TableCell>{organization.membership_count ?? 0}</TableCell>
                  <TableCell>{organization.profile_count ?? 0}</TableCell>
                  <TableCell>{organization.library_count ?? 0}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <div className="flex items-center justify-between">
        <Button
          variant="outline"
          disabled={cursorHistory.length === 1 || organizations.isFetching}
          onClick={() => setCursorHistory((current) => current.slice(0, -1))}
        >
          <ChevronLeft className="me-1 h-4 w-4" />
          {bt("bloem.admin.platformOrganizations.list.previousPage")}
        </Button>
        <span className="text-muted-foreground text-sm">
          {bt("bloem.admin.platformOrganizations.list.page", { page: cursorHistory.length })}
        </span>
        <Button
          aria-label={bt("bloem.admin.platformOrganizations.list.nextPage")}
          variant="outline"
          disabled={!organizations.data?.next_cursor || organizations.isFetching}
          onClick={() =>
            organizations.data?.next_cursor &&
            setCursorHistory((current) => [...current, organizations.data.next_cursor!])
          }
        >
          {bt("bloem.admin.platformOrganizations.list.nextPage")}
          <ChevronRight className="ms-1 h-4 w-4" />
        </Button>
      </div>

      <CreateOrganizationDialog open={createOpen} onOpenChange={setCreateOpen} />
    </section>
  );
}

function slugify(value: string): string {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}
