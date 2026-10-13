import { useParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import CollectionEditorPage from "@/pages/CollectionEditorPage";
import {
  PERSONAL_SCOPE,
  SERVER_SCOPE,
  type ScopeKind,
  type CollectionScope,
} from "@/lib/collections/scope";
import { listActorKey } from "./api";
import { ListSyncStatus } from "./ListSyncStatus";

export default function BloemCollectionEditorPage({ scope }: { scope: ScopeKind }) {
  const { id } = useParams();
  const native = (scope === "server" ? SERVER_SCOPE : PERSONAL_SCOPE) as CollectionScope;
  const list = useQuery({
    queryKey: ["bloem-linked-collection", ...listActorKey(), scope, id],
    queryFn: () => native.fetchList(),
    enabled: Boolean(id),
  });
  const collection = list.data?.collections.find((entry) => entry.id === id);
  const binding = collection?.source_config?.bloem_list_binding;
  return (
    <>
      {typeof binding === "string" ? (
        <div className="page-shell pt-4">
          <ListSyncStatus id={binding} />
        </div>
      ) : null}
      <CollectionEditorPage key={typeof binding === "string" ? binding : "manual"} scope={scope} />
    </>
  );
}
