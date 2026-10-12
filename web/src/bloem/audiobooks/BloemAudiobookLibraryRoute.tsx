import { useCallback, useEffect, useRef, useState, type ReactElement } from "react";
import { useParams, useSearchParams } from "react-router";
import type { UserLibrary } from "@/api/types";
import LibraryHeader from "@/components/LibraryHeader";
import { Tabs, TabsContent } from "@/components/ui/tabs";
import { useUserLibraries } from "@/hooks/queries/libraries";
import { useLibraryPageStatePreference } from "@/hooks/queries/libraryPageState";
import { useStartShuffle } from "@/hooks/queries/shuffles";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import {
  hasLibraryPageSearchParams,
  isAudiobookLibraryType,
  isShuffleLibraryType,
  parseLibraryPageState,
  serializeLibraryPageSearchParams,
  updateLibraryPageSearchParams,
} from "@/pages/libraryPageSearchParams";
import BloemLibraryRecommended from "./BloemLibraryRecommended";

type LibraryPageStatePreference = ReturnType<typeof useLibraryPageStatePreference>;

/**
 * Element for /library/:libraryId. The home (recommendation) tab of an
 * audiobook library is drawn here so its resume hero plays only the chosen
 * recording of a book; everything else is Silo's LibraryPage, passed in
 * unchanged: other libraries, the library and collections tabs, loading,
 * unavailable libraries, and the restore of a saved tab on entry.
 *
 * LibraryPage restores a profile's saved tab once per profile and library on
 * entry. Until that has happened (or was not needed) this route lets
 * LibraryPage run it, then owns the home tab and saves it the way LibraryPage
 * would, since LibraryPage is no longer mounted to record the return to it.
 */
export default function BloemAudiobookLibraryRoute({ libraryPage }: { libraryPage: ReactElement }) {
  const { libraryId } = useParams<{ libraryId: string }>();
  const [searchParams] = useSearchParams();
  const { data: libraries, isLoading } = useUserLibraries();
  const pageState = useLibraryPageStatePreference();
  const id = Number(libraryId);
  const library = libraries?.find((entry) => entry.id === id);
  const libraryType = library?.type ?? "";
  const audiobookLibrary = Boolean(library) && isAudiobookLibraryType(libraryType);
  const stateKey = `${pageState.ownerKey ?? "none"}:${id}`;
  const [settledKey, setSettledKey] = useState<string | null>(null);

  const savedSearch =
    Number.isFinite(id) && id > 0 ? pageState.preference.libraries[String(id)]?.search : undefined;
  // LibraryPage applies the saved search when the URL carries no library
  // state of its own and the saved one differs.
  const restorePending =
    settledKey !== stateKey &&
    (pageState.isLoading ||
      (pageState.rememberEnabled &&
        !hasLibraryPageSearchParams(searchParams) &&
        savedSearch != null &&
        savedSearch !== serializeLibraryPageSearchParams(searchParams)));

  // Recorded during render, as React does for state derived from props: the
  // restore is done for this profile and library, so later returns to the
  // home tab stay here.
  if (audiobookLibrary && !restorePending && settledKey !== stateKey) {
    setSettledKey(stateKey);
  }

  const { activeTab } = parseLibraryPageState(searchParams, libraryType);
  if (isLoading || !library || !audiobookLibrary || restorePending || activeTab !== "recommended") {
    return libraryPage;
  }
  return <AudiobookLibraryHome library={library} pageState={pageState} stateKey={stateKey} />;
}

/** LibraryPage's frame (header, tabs) around the Bloem home tab. */
function AudiobookLibraryHome({
  library,
  pageState,
  stateKey,
}: {
  library: UserLibrary;
  pageState: LibraryPageStatePreference;
  stateKey: string;
}) {
  const [searchParams, setSearchParams] = useSearchParams();
  const libraryType = library.type;
  const { browseType, queryDefinition } = parseLibraryPageState(searchParams, libraryType);
  const { startShuffle, isStarting: isStartingShuffle } = useStartShuffle();
  const [hasRenderedHero, setHasRenderedHero] = useState(false);
  const handleHeroStateChange = useCallback((rendered: boolean) => {
    setHasRenderedHero(rendered);
  }, []);
  useDocumentTitle(library.name);

  const normalized = updateLibraryPageSearchParams(
    searchParams,
    { activeTab: "recommended", browseType, queryDefinition },
    libraryType,
  );
  const normalizedSearch = normalized.toString();
  const isNormalized = normalizedSearch === searchParams.toString();

  // LibraryPage drops stray tab and filter parameters from the home tab.
  useEffect(() => {
    if (!isNormalized) {
      setSearchParams(new URLSearchParams(normalizedSearch), { replace: true });
    }
  }, [isNormalized, normalizedSearch, setSearchParams]);

  // LibraryPage records the tab a profile last used; it has unmounted by the
  // time the listener returns here, so this page records the home tab.
  const { rememberEnabled, isLoading, saveLibrarySearch } = pageState;
  const savedSearch = pageState.preference.libraries[String(library.id)]?.search;
  const submittedRef = useRef<string | null>(null);
  useEffect(() => {
    if (!rememberEnabled || isLoading || !isNormalized) return;
    const canonical = serializeLibraryPageSearchParams(new URLSearchParams(normalizedSearch));
    if (savedSearch === canonical) {
      submittedRef.current = null;
      return;
    }
    const submission = `${stateKey}:${canonical}`;
    if (submittedRef.current === submission) return;
    submittedRef.current = submission;
    // A failed write leaves the earlier saved tab; LibraryPage retries, this
    // page does not, so the next visit may open that earlier tab.
    void saveLibrarySearch(library.id, canonical).catch(() => undefined);
  }, [
    isLoading,
    isNormalized,
    library.id,
    normalizedSearch,
    rememberEnabled,
    saveLibrarySearch,
    savedSearch,
    stateKey,
  ]);

  const handleTabChange = (value: string) => {
    setSearchParams(
      updateLibraryPageSearchParams(
        searchParams,
        {
          activeTab:
            value === "library"
              ? "library"
              : value === "collections"
                ? "collections"
                : "recommended",
          browseType,
          queryDefinition,
        },
        libraryType,
      ),
    );
  };

  return (
    <div className="relative">
      <Tabs value="recommended" onValueChange={handleTabChange}>
        <LibraryHeader
          libraryName={library.name}
          libraryType={libraryType}
          overlay={hasRenderedHero}
          onShuffle={
            isShuffleLibraryType(libraryType)
              ? () => startShuffle({ kind: "library", id: String(library.id) })
              : undefined
          }
          shuffleDisabled={isStartingShuffle}
        />
        <TabsContent value="recommended" className="mt-0">
          <BloemLibraryRecommended
            libraryId={library.id}
            libraryType={libraryType}
            onHeroStateChange={handleHeroStateChange}
          />
        </TabsContent>
      </Tabs>
    </div>
  );
}
