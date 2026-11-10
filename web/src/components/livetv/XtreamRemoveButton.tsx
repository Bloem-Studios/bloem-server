import { useState } from "react";
import { Trash2 } from "lucide-react";
import {
  captureSessionIdentity,
  getProfileTokenGeneration,
  isSessionIdentityCurrent,
} from "@/api/client";
import { getProfileId } from "@/api/bloemClient";
import { useBloemT } from "@/bloem/i18n";
import { useAuth } from "@/hooks/useAuth";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { Button } from "@/components/ui/button";

type Intent = {
  id: string;
  identity: ReturnType<typeof captureSessionIdentity>;
  profileId: string | null;
  pinGeneration: number;
};

export function XtreamRemoveButton({
  id,
  pending,
  onRemove,
}: {
  id: string;
  pending: boolean;
  onRemove: (id: string) => void;
}) {
  const bt = useBloemT();
  const { user } = useAuth();
  const [intent, setIntent] = useState<Intent | null>(null);
  function current(value: Intent) {
    return (
      value.id === id &&
      isSessionIdentityCurrent(value.identity) &&
      value.profileId === getProfileId() &&
      value.pinGeneration === getProfileTokenGeneration()
    );
  }
  if (user?.role !== "admin") return null;
  return (
    <>
      <Button
        variant="outline"
        size="sm"
        disabled={pending}
        onClick={() =>
          setIntent({
            id,
            identity: captureSessionIdentity(),
            profileId: getProfileId(),
            pinGeneration: getProfileTokenGeneration(),
          })
        }
      >
        <Trash2 />
        {bt("bloem.admin.liveTv.xtreamRemove.remove")}
      </Button>
      <ConfirmDialog
        open={Boolean(intent && current(intent))}
        onOpenChange={(open) => {
          if (!open) setIntent(null);
        }}
        title={bt("bloem.admin.liveTv.xtreamRemove.confirmTitle")}
        description={bt("bloem.admin.liveTv.xtreamRemove.confirmDescription")}
        confirmLabel={bt("bloem.admin.liveTv.xtreamRemove.confirmRemove")}
        variant="destructive"
        isPending={pending}
        onConfirm={() => {
          const captured = intent;
          setIntent(null);
          if (captured && current(captured) && !pending) onRemove(captured.id);
        }}
      />
    </>
  );
}
