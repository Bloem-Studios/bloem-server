import {
  captureProviderEditIntent,
  getProviderEditor,
  providerIntentActive,
  providerSaveMessage,
  type ProviderEditor,
} from "@/api/v2/adminSubtitleProviderConfiguration";
import { useState } from "react";
import type { components } from "@/api/v2/schema";
type SubtitleProviderConfig = components["schemas"]["AdminSubtitleProvider"];
import {
  useSubtitleProviders,
  useUpdateSubtitleProvider,
  useTestSubtitleProvider,
} from "@/hooks/queries/admin/subtitles";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Skeleton } from "@/components/ui/skeleton";
import { CircleCheck, Eye, EyeOff, Loader2 } from "lucide-react";
import { useWizardContext } from "../WizardContext";

// --- Provider metadata ---

const PROVIDER_META: Record<string, { name: string; description: string }> = {
  opensubtitles: {
    name: "OpenSubtitles",
    description: "Largest subtitle database. Requires a free account.",
  },
  subdl: {
    name: "SubDL",
    description: "Fast, modern subtitle API with generous free tier.",
  },
  subsource: {
    name: "SubSource",
    description: "Community-driven subtitle source.",
  },
};

const SUBTITLE_PROVIDER_ORDER = ["opensubtitles", "subdl", "subsource"];

// --- Provider card ---

function ProviderCard({ config, scope }: { config: SubtitleProviderConfig; scope: string }) {
  const [editor, setEditor] = useState<ProviderEditor | null>(null);
  const [loading, setLoading] = useState(false);
  const [attempted, setAttempted] = useState(false);
  const [feedback, setFeedback] = useState("");
  const [enabled, setEnabled] = useState(config.enabled);
  const [apiKey, setApiKey] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [showApiKey, setShowApiKey] = useState(false);

  const updateProvider = useUpdateSubtitleProvider();
  const testProvider = useTestSubtitleProvider();
  const [testResult, setTestResult] = useState<{ success: boolean; error?: string } | null>(null);

  const providerName = config.provider_name;
  const meta = PROVIDER_META[providerName] ?? { name: providerName, description: "" };
  const isOpenSubtitles = providerName === "opensubtitles";
  const canonical = editor?.body ?? config;
  const hasCredentials = isOpenSubtitles ? canonical.has_credentials : canonical.has_api_key;

  async function loadEditor() {
    if (loading || updateProvider.isPending) return;
    setLoading(true);
    setFeedback("");
    try {
      const loaded = await getProviderEditor(captureProviderEditIntent(providerName, scope));
      if (!providerIntentActive(loaded.intent)) return;
      setEditor(loaded);
      setEnabled(loaded.body.enabled);
      setApiKey("");
      setUsername("");
      setPassword("");
      setTestResult(null);
      setAttempted(false);
    } catch {
      setEditor(null);
      setFeedback("Unable to load saved configuration. Reload before editing.");
    } finally {
      setLoading(false);
    }
  }
  function handleSave() {
    if (!editor || attempted || loading || updateProvider.isPending) return;
    setAttempted(true);
    updateProvider.mutate(
      {
        editor,
        config: { enabled, ...(isOpenSubtitles ? { username, password } : { api_key: apiKey }) },
      },
      {
        onSuccess: (saved) => {
          if (!providerIntentActive(editor.intent)) return;
          setApiKey("");
          setUsername("");
          setPassword("");
          setTestResult(null);
          setFeedback(providerSaveMessage(saved));
        },
        onError: () => {
          if (!providerIntentActive(editor.intent)) return;
          setFeedback(
            "Save not confirmed. Your draft is retained. Reload saved configuration before making another change.",
          );
        },
      },
    );
  }

  function handleTest() {
    setTestResult(null);
    testProvider.mutate(
      {
        provider: providerName,
        config: {
          enabled,
          ...(isOpenSubtitles ? { username, password } : { api_key: apiKey }),
        },
      },
      {
        onSuccess: (result) => setTestResult({ success: result.success, error: result.error }),
        onError: (err) =>
          setTestResult({
            success: false,
            error: err instanceof Error ? err.message : "Test failed",
          }),
      },
    );
  }

  return (
    <div className="bg-foreground/[0.03] hover:bg-foreground/[0.05] border-foreground/[0.07] rounded-xl border transition-colors">
      {feedback && (
        <p role="status" className="px-4 py-2 text-sm">
          {feedback}
        </p>
      )}
      <Button
        variant="outline"
        size="sm"
        disabled={loading || updateProvider.isPending || testProvider.isPending}
        onClick={() => void loadEditor()}
      >
        {loading
          ? "Loading saved configuration..."
          : editor
            ? "Reload saved configuration (discard draft)"
            : `Configure ${meta.name}`}
      </Button>
      <fieldset
        disabled={
          !editor || loading || attempted || updateProvider.isPending || testProvider.isPending
        }
      >
        {/* Card header */}
        <div className="flex items-center justify-between px-4 py-3.5">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <span className="text-sm font-medium">{meta.name}</span>
              {hasCredentials && <CircleCheck className="h-3.5 w-3.5 shrink-0 text-green-500" />}
            </div>
            <p className="text-muted-foreground mt-0.5 text-xs">{meta.description}</p>
          </div>
          <Switch
            id={`${providerName}-enabled`}
            aria-label={`Enable ${meta.name}`}
            checked={enabled}
            onCheckedChange={setEnabled}
            className="ml-4 shrink-0"
          />
        </div>

        {/* Credentials — shown when enabled */}
        {enabled && (
          <div className="border-foreground/[0.06] border-t px-4 py-3.5">
            {isOpenSubtitles ? (
              <div className="grid gap-2.5 sm:grid-cols-2">
                <Input
                  type="text"
                  placeholder={canonical.has_credentials ? "Leave blank to keep" : "Username"}
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  className="h-8 text-sm"
                />
                <Input
                  type="password"
                  placeholder={canonical.has_credentials ? "Leave blank to keep" : "Password"}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  className="h-8 text-sm"
                />
              </div>
            ) : (
              <div className="flex items-center gap-2">
                <Input
                  type={showApiKey ? "text" : "password"}
                  placeholder={canonical.has_api_key ? "Leave blank to keep" : "API key"}
                  value={apiKey}
                  onChange={(e) => setApiKey(e.target.value)}
                  className="h-8 flex-1 text-sm"
                />
                <button
                  type="button"
                  className="text-muted-foreground hover:text-foreground flex h-8 w-8 shrink-0 items-center justify-center rounded-md transition-colors"
                  onClick={() => setShowApiKey(!showApiKey)}
                >
                  {showApiKey ? (
                    <EyeOff className="h-3.5 w-3.5" />
                  ) : (
                    <Eye className="h-3.5 w-3.5" />
                  )}
                </button>
              </div>
            )}

            {/* Actions */}
            <div className="mt-3 flex items-center gap-2">
              <Button
                variant="secondary"
                size="sm"
                className="h-7 px-3 text-xs"
                onClick={handleTest}
                disabled={testProvider.isPending}
              >
                {testProvider.isPending ? (
                  <>
                    <Loader2 className="mr-1.5 h-3 w-3 animate-spin" />
                    Testing
                  </>
                ) : (
                  "Test connection"
                )}
              </Button>
              {testResult !== null && (
                <span
                  className={`text-xs ${testResult.success ? "text-green-500" : "text-red-400"}`}
                >
                  {testResult.success ? "Connected" : (testResult.error ?? "Failed")}
                </span>
              )}
            </div>
          </div>
        )}
        <Button
          variant="secondary"
          size="sm"
          onClick={handleSave}
          disabled={updateProvider.isPending}
        >
          {updateProvider.isPending ? "Saving..." : "Save"}
        </Button>
      </fieldset>
    </div>
  );
}

// --- Main step ---

export function IntegrationsStep() {
  const { markDone } = useWizardContext();
  const { data, isLoading, scope } = useSubtitleProviders();

  if (isLoading) {
    return (
      <div className="space-y-3">
        {Array.from({ length: 3 }).map((_, i) => (
          <Skeleton key={i} className="h-[4.5rem] w-full rounded-xl" />
        ))}
      </div>
    );
  }

  const providers = data?.providers ?? [];
  const sorted = [...providers].sort((a, b) => {
    const ai = SUBTITLE_PROVIDER_ORDER.indexOf(a.provider_name);
    const bi = SUBTITLE_PROVIDER_ORDER.indexOf(b.provider_name);
    if (ai === -1 && bi === -1) return 0;
    if (ai === -1) return 1;
    if (bi === -1) return -1;
    return ai - bi;
  });

  return (
    <div className="space-y-6">
      {/* Provider cards */}
      {sorted.length > 0 ? (
        <div className="space-y-2.5">
          {sorted.map((provider) => (
            <ProviderCard
              key={`${scope}:${provider.provider_name}`}
              config={provider}
              scope={scope}
            />
          ))}
        </div>
      ) : (
        <p className="text-muted-foreground py-6 text-center text-sm">
          No subtitle providers available.
        </p>
      )}

      <div className="flex gap-3 pt-2">
        <Button onClick={() => markDone("integrations")}>Continue</Button>
        <Button variant="ghost" onClick={() => markDone("integrations")}>
          Skip
        </Button>
      </div>
    </div>
  );
}
