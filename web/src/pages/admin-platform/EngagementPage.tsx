import { useBloemT } from "@/bloem/i18n";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { adminV2Api, adminV2QueryKey, captureAdminRequestContext } from "@/api/adminV2Client";
import type { AdminContextSummary } from "@/api/bloemTypes";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import { useBloemCapabilities } from "@/hooks/queries/useBloemCapabilities";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { CampaignEditor, SeasonEditor } from "@/components/engagement/EngagementEditors";
import type {
  CampaignInput,
  SeasonalInput,
  StoredCampaign,
  StoredSeason,
} from "@/components/engagement/campaigns";
import { randomUUID } from "@/lib/uuid";

export default function EngagementPage() {
  const bt = useBloemT();
  const { active } = useAdminContext();
  useDocumentTitle(bt("bloem.admin.engagement.title"));
  if (active?.scope !== "platform")
    return <p role="alert">{bt("bloem.admin.engagement.selectPlatformContext")}</p>;
  return (
    <EngagementRegistry
      key={`${active.key}:${captureAdminRequestContext(active.key)?.generation ?? "unbound"}`}
      context={active}
    />
  );
}
function EngagementRegistry({ context }: { context: AdminContextSummary }) {
  const bt = useBloemT();
  const capabilities = useBloemCapabilities();
  const supported = Boolean(
    capabilities.data?.feature_tokens?.includes("platform_engagement_authoring_v1"),
  );
  const authority = captureAdminRequestContext(context.key);
  const client = useQueryClient();
  const prefix = adminV2QueryKey(context.key, "engagement", authority?.generation);
  const campaigns = useQuery({
    queryKey: [...prefix, "promotions"],
    enabled: supported,
    queryFn: ({ signal }) =>
      adminV2Api<{ promotions: StoredCampaign[] }>(
        "/platform/promotions",
        { signal },
        "safe",
        authority,
      ),
    retry: false,
  });
  const seasons = useQuery({
    queryKey: [...prefix, "ambience"],
    enabled: supported,
    queryFn: ({ signal }) =>
      adminV2Api<{
        packs: StoredSeason[];
        storage_available: boolean;
        yearly_scheduling?: boolean;
      }>("/platform/ambience", { signal }, "safe", authority),
    retry: false,
  });
  const [tab, setTab] = useState("promotions");
  const [editing, setEditing] = useState<
    | { kind: "promotions"; value?: StoredCampaign }
    | { kind: "ambience"; value?: StoredSeason }
    | null
  >(null);
  const [deleting, setDeleting] = useState<{
    kind: "promotions" | "ambience";
    id: string;
    name: string;
  } | null>(null);
  const [error, setError] = useState("");
  const write = useMutation({
    networkMode: "always",
    retry: false,
    mutationFn: ({ path, init }: { path: string; init: RequestInit }) =>
      adminV2Api<unknown>(path, init, "none", authority),
    onSettled: () => client.invalidateQueries({ queryKey: prefix }),
  });
  async function save(value: CampaignInput | SeasonalInput) {
    if (!editing || write.isPending) return;
    setError("");
    try {
      await write.mutateAsync({
        path: `/platform/${editing.kind}${editing.value ? `/${encodeURIComponent(editing.value.id)}` : ""}`,
        init: { method: editing.value ? "PUT" : "POST", body: JSON.stringify(value) },
      });
      setEditing(null);
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : bt("bloem.admin.engagement.publishUnconfirmed"),
      );
      throw cause;
    }
  }
  async function upload(
    file: File,
    kind: "campaign_card_16x9" | "season_banner" | "season_sprite",
  ) {
    const body = new FormData();
    body.set("file", file);
    body.set("kind", kind);
    body.set("asset_id", randomUUID());
    const result = (await write.mutateAsync({
      path: "/platform/ambience/assets",
      init: { method: "POST", body },
    })) as { url?: string };
    if (!result.url) throw new Error(bt("bloem.admin.engagement.noArtworkUrl"));
    return result.url;
  }
  async function remove() {
    if (!deleting || write.isPending) return;
    setError("");
    try {
      await write.mutateAsync({
        path: `/platform/${deleting.kind}/${encodeURIComponent(deleting.id)}`,
        init: { method: "DELETE" },
      });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : bt("bloem.admin.engagement.deleteFailed"));
    } finally {
      setDeleting(null);
    }
  }
  if (capabilities.isPending)
    return <p role="status">{bt("bloem.admin.engagement.checkingSupport")}</p>;
  if (capabilities.isError)
    return (
      <div role="alert">
        <p>{capabilities.error.message}</p>
        <Button onClick={() => void capabilities.refetch()}>
          {bt("bloem.admin.engagement.tryAgain")}
        </Button>
      </div>
    );
  if (!supported) return <p>{bt("bloem.admin.engagement.unsupported")}</p>;
  const query = tab === "promotions" ? campaigns : seasons;
  return (
    <section className="admin-page space-y-6">
      <header className="space-y-2">
        <h1 className="page-title" tabIndex={-1}>
          {bt("bloem.admin.engagement.title")}
        </h1>
        <p className="page-subtitle">{bt("bloem.admin.engagement.subtitle")}</p>
      </header>
      {error && (
        <p role="alert" className="text-destructive">
          {error}
        </p>
      )}
      {editing ? (
        <div className="border-border rounded-xl border p-5">
          <p className="text-muted-foreground mb-5 text-sm">
            {bt("bloem.admin.engagement.editorNote")}
          </p>
          {editing.kind === "promotions" ? (
            <CampaignEditor
              key={editing.value?.id ?? "new-campaign"}
              initial={editing.value}
              pending={write.isPending}
              storageAvailable={Boolean(seasons.data?.storage_available)}
              upload={upload}
              onCancel={() => setEditing(null)}
              onSave={save}
            />
          ) : (
            <SeasonEditor
              key={editing.value?.id ?? "new-season"}
              initial={editing.value}
              pending={write.isPending}
              storageAvailable={Boolean(seasons.data?.storage_available)}
              yearlyAvailable={Boolean(seasons.data?.yearly_scheduling)}
              upload={upload}
              onCancel={() => setEditing(null)}
              onSave={save}
            />
          )}
        </div>
      ) : (
        <Tabs
          value={tab}
          onValueChange={(value) => {
            setTab(value);
            setError("");
          }}
        >
          <TabsList>
            <TabsTrigger value="promotions">
              {bt("bloem.admin.engagement.tabCampaigns")}
            </TabsTrigger>
            <TabsTrigger value="ambience">
              {bt("bloem.admin.engagement.tabSeasonalPacks")}
            </TabsTrigger>
          </TabsList>
          {query.isPending && <p role="status">{bt("bloem.admin.engagement.loading")}</p>}
          {query.isError && (
            <div role="alert" className="my-4 space-y-2">
              <p>{query.error.message}</p>
              <Button variant="outline" onClick={() => void query.refetch()}>
                {bt("bloem.admin.engagement.tryAgain")}
              </Button>
            </div>
          )}
          <TabsContent value="promotions" className="space-y-4">
            <Button
              disabled={!campaigns.isSuccess || write.isPending}
              onClick={() => setEditing({ kind: "promotions" })}
            >
              {bt("bloem.admin.engagement.createCampaign")}
            </Button>
            {campaigns.isSuccess && !campaigns.data.promotions.length && (
              <p className="text-muted-foreground">{bt("bloem.admin.engagement.noCampaigns")}</p>
            )}
            <ul className="divide-border divide-y">
              {campaigns.data?.promotions.map((item) => (
                <li key={item.id} className="flex flex-wrap justify-between gap-4 py-4">
                  <div>
                    <h2 className="font-medium">{item.headline}</h2>
                    <p className="text-muted-foreground text-sm">
                      {bt("bloem.admin.engagement.campaignSummary", {
                        surfaces: item.surfaces.join(", "),
                        startsAt: item.starts_at,
                        endsAt: item.ends_at,
                      })}
                    </p>
                  </div>
                  <div className="flex gap-2">
                    <Button
                      variant="outline"
                      disabled={write.isPending}
                      onClick={() => setEditing({ kind: "promotions", value: item })}
                    >
                      {bt("bloem.admin.engagement.edit")}
                    </Button>
                    <Button
                      variant="destructive"
                      disabled={write.isPending}
                      onClick={() =>
                        setDeleting({ kind: "promotions", id: item.id, name: item.headline })
                      }
                    >
                      {bt("bloem.admin.engagement.delete")}
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
          </TabsContent>
          <TabsContent value="ambience" className="space-y-4">
            <Button
              disabled={!seasons.isSuccess || write.isPending}
              onClick={() => setEditing({ kind: "ambience" })}
            >
              {bt("bloem.admin.engagement.createSeasonalPack")}
            </Button>
            {seasons.isSuccess && !seasons.data.packs.length && (
              <p className="text-muted-foreground">
                {bt("bloem.admin.engagement.noSeasonalPacks")}
              </p>
            )}
            <ul className="divide-border divide-y">
              {seasons.data?.packs.map((item) => (
                <li key={item.id} className="flex flex-wrap justify-between gap-4 py-4">
                  <div>
                    <h2 className="font-medium">{item.effect_id}</h2>
                    <p className="text-muted-foreground text-sm">
                      {bt("bloem.admin.engagement.seasonSummary", {
                        surfaces: item.surfaces.join(", "),
                        repeat: item.window.repeat_yearly ? "yearly" : "once",
                        timezone: item.window.timezone || "UTC",
                      })}
                    </p>
                  </div>
                  <div className="flex gap-2">
                    <Button
                      variant="outline"
                      disabled={write.isPending}
                      onClick={() => setEditing({ kind: "ambience", value: item })}
                    >
                      {bt("bloem.admin.engagement.edit")}
                    </Button>
                    <Button
                      variant="destructive"
                      disabled={write.isPending}
                      onClick={() =>
                        setDeleting({ kind: "ambience", id: item.id, name: item.effect_id })
                      }
                    >
                      {bt("bloem.admin.engagement.delete")}
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
          </TabsContent>
        </Tabs>
      )}
      <ConfirmDialog
        open={Boolean(deleting)}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={
          deleting
            ? bt("bloem.admin.engagement.deleteNamed", { name: deleting.name })
            : bt("bloem.admin.engagement.deleteEntry")
        }
        description={bt("bloem.admin.engagement.deleteDescription")}
        confirmLabel={bt("bloem.admin.engagement.delete")}
        variant="destructive"
        isPending={write.isPending}
        onConfirm={() => void remove()}
      />
    </section>
  );
}
