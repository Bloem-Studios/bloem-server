import { usePreviewMode } from "@/bloem/preview/usePreviewMode";
import PreviewCollections from "@/bloem/preview/PreviewCollections";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import Collections from "@/pages/Collections";
import AdminCollections from "@/pages/AdminCollections";
import { Button } from "@/components/ui/button";
import { listCollectionsApi, listActorKey } from "./api";
import { ListSourceDialog } from "./ListSourceDialog";
import { ListSyncStatus } from "./ListSyncStatus";

function SourceAction({ scope }: { scope: "personal" | "server" }) {
  const [open, setOpen] = useState(false);
  const [created, setCreated] = useState("");
  const capability = useQuery({
    queryKey: ["bloem-list-capability", ...listActorKey()],
    queryFn: listCollectionsApi.capabilities,
    retry: false,
    staleTime: 30000,
  });
  if (
    !capability.data?.available ||
    (scope === "server" && !capability.data.connections.some((entry) => entry.can_create_server))
  )
    return null;
  const authorized = {
    ...capability.data,
    connections: capability.data.connections.filter(
      (entry) => scope !== "server" || entry.can_create_server,
    ),
  };
  return (
    <div className="page-shell pt-4">
      <div className="mb-3 flex justify-end">
        <Button variant="outline" onClick={() => setOpen(true)}>
          Link Pastime list
        </Button>
      </div>
      {created ? <ListSyncStatus id={created} /> : null}
      {open ? (
        <ListSourceDialog
          capability={authorized}
          scope={scope}
          onClose={() => setOpen(false)}
          onCreated={(binding) => setCreated(binding.id)}
        />
      ) : null}
    </div>
  );
}
export default function BloemCollections() {
  return usePreviewMode().active ? <PreviewCollections /> : <SubscriberCollections />;
}
function SubscriberCollections() {
  return (
    <>
      <SourceAction scope="personal" />
      <Collections />
    </>
  );
}
export function BloemAdminCollections() {
  return (
    <>
      <SourceAction scope="server" />
      <AdminCollections />
    </>
  );
}
