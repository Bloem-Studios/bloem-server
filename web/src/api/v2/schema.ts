export interface paths {
  "/api/v2/account/me": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Get the authenticated caller's login account. */
    get: operations["getCurrentUser"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/admin/users": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** List login accounts with their policy overrides and effective policy, in account id order. */
    get: operations["listAdminUsers"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/audio-prefs/{series_id}": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Get the acting profile's remembered audio track for a series. */
    get: operations["getAudioPreference"];
    /** Replace the acting profile's remembered audio track for a series. */
    put: operations["updateAudioPreference"];
    post?: never;
    /** Forget the acting profile's remembered audio track for a series. */
    delete: operations["deleteAudioPreference"];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/calendar": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Upcoming and recent airings and releases in a window of the viewer's local days, grouped by day. */
    get: operations["getCalendar"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/favorites": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** List the acting profile's favorites as catalog cards, newest favorite first; items the viewer may not see are omitted. */
    get: operations["listFavorites"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/favorites/{item_id}": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Answer whether the item is one of the acting profile's favorites: the entry, or 404 when it is not (or the viewer may not see it). */
    get: operations["getFavorite"];
    /** Add the item to the acting profile's favorites. Automatic retries are unsafe because provider and refresh effects are not change-gated. */
    put: operations["addFavorite"];
    post?: never;
    /** Remove the item from the acting profile's favorites; an absent entry succeeds, but automatic retries can repeat provider and refresh effects. */
    delete: operations["deleteFavorite"];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/history": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** List the acting profile's watch history as catalog cards, most recent watch first. */
    get: operations["listHistory"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/history/remove": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** Hide the targets' watches from the acting profile's history; the server chooses the cutoff when this request runs. */
    post: operations["removeHistoryEntries"];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/home/dismissals/{surface}/{item_id}": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    /** Hide a card from Continue Watching or Next Up for the acting profile; repeating it refreshes the dismissal. */
    put: operations["dismissHomeItem"];
    post?: never;
    /** Show a dismissed card again; an item that was not dismissed is left as is. */
    delete: operations["undismissHomeItem"];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/home/layout": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** The home page's section layout for the acting profile, without items. */
    get: operations["getHomeLayout"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/home/sections": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** The home page's sections with their cards, as the acting profile sees them. */
    get: operations["listHomeSections"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/home/sections/{id}/items": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** One section of the home page with its cards. */
    get: operations["getHomeSectionItems"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/libraries": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** List every library in sort order, with its presigned poster URL. */
    get: operations["listLibraries"];
    put?: never;
    /** Create a library, seed its sections and provider chain, and queue its first scan. */
    post: operations["createLibrary"];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/libraries/{id}": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post?: never;
    /** Disable a library and queue the job that deletes it and its items; answers 202 with the job. */
    delete: operations["deleteLibrary"];
    options?: never;
    head?: never;
    /** Update a library; omitted members are unchanged. A changed path set queues a rescan and a changed language a quick metadata refresh. */
    patch: operations["updateLibrary"];
    trace?: never;
  };
  "/api/v2/libraries/{id}/check-mount": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** Probe every root of a library; a healthy result clears an outstanding empty-root or dead-root warning. */
    post: operations["checkLibraryMount"];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/libraries/{id}/confirm-empty-root-cleanup": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** Arm the library's next scan to clean up an empty root once instead of treating it as a lost mount. */
    post: operations["confirmEmptyRootCleanup"];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/libraries/{id}/metadata-match-queue": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** One library's matcher backlog counts with a page of its queued movies, series roots and raw files. */
    get: operations["getMetadataMatchQueue"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/libraries/{id}/metadata-match-queue/cancel": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** Drop every queued matcher entry of the library and suppress its raw backlog; answers what was canceled. */
    post: operations["cancelMetadataMatchQueue"];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/libraries/{id}/metadata-match-queue/retry": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** Re-sync and immediately retry every queued matcher entry of the library; answers the counts afterwards. */
    post: operations["retryMetadataMatchQueue"];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/libraries/{id}/poster": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    /** Store a poster for the library from a multipart upload (JPEG, PNG or WebP, at most 10 MiB) and answer the library with its new poster URL. */
    put: operations["uploadLibraryPoster"];
    post?: never;
    /** Remove the library's poster; a library without one is left as is. */
    delete: operations["deleteLibraryPoster"];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/libraries/{id}/providers": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** The library's metadata provider chain, per content level. Legacy unlevelled rows (content_level '') that an upgraded database keeps are not exposed; setLibraryProviders preserves them. */
    get: operations["getLibraryProviders"];
    /** Replace the library's whole provider chain and wake the matcher. Legacy unlevelled rows (content_level '') are kept as they are; a level not listed ends up with no providers. */
    put: operations["setLibraryProviders"];
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/libraries/{id}/refresh-metadata": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** Queue a metadata refresh of the library's items; answers 202 with the job. */
    post: operations["refreshLibraryMetadata"];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/libraries/metadata-match-queue": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** List the metadata matcher backlog of every library. */
    get: operations["listMetadataMatchQueues"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/libraries/provider-defaults": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** The provider chain a new library of a type would be seeded with, per content level. */
    get: operations["getLibraryProviderDefaults"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/libraries/reorder": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** Assign libraries their sort positions. */
    post: operations["reorderLibraries"];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/libraries/roots": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Page the scanned content roots of one library with their active overrides. */
    get: operations["listLibraryRoots"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/libraries/roots/override": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    /** Set the identity override on a scanned root, replacing any existing one. */
    put: operations["setRootOverride"];
    post?: never;
    /** Remove the identity override on a scanned root. */
    delete: operations["deleteRootOverride"];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/libraries/skipped-roots": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Page roots the scanner skipped, across libraries. */
    get: operations["listSkippedRoots"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/libraries/stale-ids": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** List provider identifiers that no longer resolve, with the items carrying them, a page at a time. */
    get: operations["listStaleIds"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/libraries/stale-ids/{content_id}/rematch": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** Clear an item's provider identifiers and refresh its metadata in the background. */
    post: operations["rematchStaleId"];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/libraries/unmatched-items": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Page the catalog items awaiting a metadata match, in title order. */
    get: operations["listUnmatchedItems"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/library-jobs/{job_id}": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Poll accepted library work. Terminal jobs remain available for at least 24 hours. */
    get: operations["getLibraryJob"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/library-jobs/{job_id}/cancel": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** Request best-effort cancellation of metadata refresh. Completed metadata changes remain. Deletion cannot be canceled. */
    post: operations["cancelLibraryJob"];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/library-playback-prefs": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** List the acting profile's per-library playback overrides. */
    get: operations["listLibraryPlaybackPreferences"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/library-playback-prefs/{library_id}": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post?: never;
    /** Remove the acting profile's playback overrides for a library. */
    delete: operations["deleteLibraryPlaybackPreference"];
    options?: never;
    head?: never;
    /** Change the acting profile's playback overrides for a library: omitted members are unchanged, null clears one. */
    patch: operations["updateLibraryPlaybackPreference"];
    trace?: never;
  };
  "/api/v2/library/{id}/collections": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** The library's Collections tab: curated collections, their groups, and the viewer's opted-in personal collections. */
    get: operations["getLibraryCollections"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/library/{id}/layout": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** The library's section layout for the acting profile, without items. */
    get: operations["getLibraryLayout"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/library/{id}/sections": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** The library's sections with their cards, as the acting profile sees them. */
    get: operations["listLibrarySections"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/library/{id}/sections/{section_id}/items": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** One section of the library with its cards. */
    get: operations["getLibrarySectionItems"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/library/{id}/user-collections": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** The viewer's own personal collections opted into this library's tab. */
    get: operations["listLibraryUserCollections"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/openapi.json": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Get the committed OpenAPI document this server was built from. */
    get: operations["getOpenAPIDocument"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/profile/sections": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** List the acting profile's saved section overrides for one page. */
    get: operations["listProfileSectionOverrides"];
    /** Replace the acting profile's section overrides for one page. */
    put: operations["replaceProfileSectionOverrides"];
    post?: never;
    /** Delete the acting profile's section overrides for one page, restoring the admin layout. */
    delete: operations["resetProfileSectionOverrides"];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/profile/sections/flags": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Get what this server lets profiles do to their pages. */
    get: operations["getProfileSectionFlags"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/profile/sections/settings": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Get one page's sections as the acting profile sees them, with its overrides applied. */
    get: operations["getProfileSectionSettings"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/profiles": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** List the household profiles on the signed-in account. */
    get: operations["listProfiles"];
    put?: never;
    /** Create a household profile. */
    post: operations["createProfile"];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/profiles/{id}": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post?: never;
    /** Delete a household profile; the primary profile cannot be deleted. */
    delete: operations["deleteProfile"];
    options?: never;
    head?: never;
    /** Update a household profile; omitted members are unchanged. */
    patch: operations["updateProfile"];
    trace?: never;
  };
  "/api/v2/profiles/{id}/avatar": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    /** Replace a profile's avatar with an uploaded image (multipart form, part `avatar`: JPEG, PNG or WebP, at most 10 MiB; the whole request, framing included, at most 11 MiB). */
    put: operations["uploadProfileAvatar"];
    post?: never;
    /** Remove a profile's uploaded avatar; a preset avatar is left as is. */
    delete: operations["deleteProfileAvatar"];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/profiles/{id}/verify-pin": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** Check a profile's PIN; a match issues the X-Profile-Token that unlocks the profile for this login session. */
    post: operations["verifyProfilePIN"];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/profiles/household/sessions": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** List the live playback sessions on the signed-in account, for a household manager. */
    get: operations["listHouseholdSessions"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/progress": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** List the acting profile's watch progress, newest change first. */
    get: operations["listProgress"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/ratings": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** List the acting profile's ratings, most recently rated first. */
    get: operations["listRatings"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/ratings/{item_id}": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Answer the acting profile's rating of the item, or 404 when the profile has not rated it. */
    get: operations["getRating"];
    /** Set the acting profile's rating of the item, replacing any earlier rating; automatic retries repeat the rating timestamp and recommendation refresh. */
    put: operations["setRating"];
    post?: never;
    /** Remove the acting profile's rating of the item; an absent rating succeeds, but automatic retries repeat the recommendation refresh. */
    delete: operations["deleteRating"];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/recommendations/because-watched/{item_id}": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Cards recommended because the acting profile watched the item, minus what it has watched or rated low. */
    get: operations["listBecauseWatched"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/recommendations/discover": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** The Discover page: the acting profile's recommendation rows with their cards, blended with upcoming airings. */
    get: operations["getDiscover"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/recommendations/for-you/main": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** The acting profile's main For You row; empty until the engine has a profile for it. */
    get: operations["getForYouMain"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/recommendations/for-you/rows": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** The acting profile's For You rows, one per taste cluster. */
    get: operations["listForYouRows"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/recommendations/popular": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** The server's most played items over the window, minus what the acting profile has watched. */
    get: operations["listPopular"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/recommendations/recently-added": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Items added over the window, minus what the acting profile has watched. */
    get: operations["listRecentlyAdded"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/recommendations/section/{kind}": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** One recommendation row in full, for a dedicated see-all page; empty when the profile has no such row. */
    get: operations["getRecommendationSection"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/recommendations/similar-users": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Cards profiles with similar taste liked. */
    get: operations["listSimilarUsersLiked"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/recommendations/similar/{item_id}": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Cards similar to the item by content; not personalised beyond the acting profile's access. */
    get: operations["listSimilar"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/recommendations/taste-profile": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** The acting profile's taste summary; empty members until the engine has computed one. */
    get: operations["getTasteProfile"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/recommendations/taste-seed": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** Record the picked items as the acting profile's favorites and queue a taste-profile refresh. */
    post: operations["createTasteSeed"];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/recommendations/taste-seed/items": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** A page of cards for the taste-seeding picker, most recognizable first. */
    get: operations["listTasteSeedItems"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/recommendations/watch-tonight": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** The acting profile's Watch Tonight list: in-progress and next-up items merged with taste candidates, best first. */
    get: operations["getWatchTonight"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/recommendations/watch-tonight/cards": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** A page of Watch Tonight swipe cards with cast, in continue or discover mode, minus the identifiers already swiped. */
    get: operations["listWatchTonightCards"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/sections/recipes": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** The section recipe gallery: every visible recipe with its presets, grouped by category. */
    get: operations["listSectionRecipes"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/sections/recipes/{type}/candidates": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** The values a parameterized recipe offers for its parameter. */
    get: operations["listSectionRecipeCandidates"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/settings/contract": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Get the public settings manifest this server was built with. */
    get: operations["getSettingsContract"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/settings/contract/capabilities": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Get what this server's settings API supports. */
    get: operations["getSettingsContractCapabilities"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/settings/device/subtitle-appearance": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    /** Replace this device's subtitle appearance override for the acting profile. */
    put: operations["updateSubtitleAppearanceDeviceOverride"];
    post?: never;
    /** Remove this device's subtitle appearance override for the acting profile. */
    delete: operations["deleteSubtitleAppearanceDeviceOverride"];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/settings/overlay-config": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Get the server-wide card overlay defaults. */
    get: operations["getOverlayConfig"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/settings/plugins": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** List the enabled plugins that expose user settings or navigable routes. */
    get: operations["listPluginSettings"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/settings/plugins/{installation_id}": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Get a plugin installation's user settings and the account's values for them. */
    get: operations["getPluginSettings"];
    /** Replace the account's values for a plugin installation's user settings. */
    put: operations["updatePluginSettings"];
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/settings/subtitle-appearance/effective": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Get the subtitle appearance that applies to the acting profile on this device. */
    get: operations["getEffectiveSubtitleAppearance"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/settings/values": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Get the explicit values of several settings at one scope. */
    get: operations["listSettingValues"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/settings/values/{key}": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Get the explicit value of a setting at one scope. */
    get: operations["getSettingValue"];
    /** Replace the explicit value of a setting at one scope. */
    put: operations["updateSettingValue"];
    post?: never;
    /** Remove the explicit value of a setting at one scope so it inherits again. */
    delete: operations["deleteSettingValue"];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/settings/values/effective": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Resolve the effective values of settings for the acting profile. */
    get: operations["listEffectiveSettings"];
    put?: never;
    /** Resolve the effective values of settings under several content contexts at once. */
    post: operations["resolveEffectiveSettings"];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/settings/values/nav.shortcuts/item": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    /** Add or remove one navigation shortcut of the acting profile. */
    put: operations["updateNavigationShortcut"];
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/subtitle-prefs/{series_id}": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Get the acting profile's remembered subtitle choice for a series. */
    get: operations["getSubtitlePreference"];
    /** Replace the acting profile's remembered subtitle choice for a series. */
    put: operations["updateSubtitlePreference"];
    post?: never;
    /** Forget the acting profile's remembered subtitle choice for a series. */
    delete: operations["deleteSubtitlePreference"];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/sync/progress": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** Apply a batch of progress writes for the acting profile and answer one result per item; supply updated_at on each item to preserve event ordering. */
    post: operations["syncProgress"];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/system/info": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Get server and contract identity for discovery before login. */
    get: operations["getSystemInfo"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/system/setup": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Report whether the server still needs its first administrator. */
    get: operations["getSetupStatus"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/watch/{id}": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Get what is needed to play an item: its versions, subtitles, markers and the acting profile's progress. */
    get: operations["getWatchState"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/watched/{id}": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** Mark an item watched for the acting profile; a season or series marks every episode. Marking an already watched item is a no-op. */
    post: operations["markWatched"];
    /** Mark an item unwatched for the acting profile; a season or series clears every episode. The server chooses the history cutoff when this request runs. */
    delete: operations["unmarkWatched"];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/watchlist": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** List the acting profile's watchlist as catalog cards, newest entry first; fully-watched series and items the viewer may not see are omitted. */
    get: operations["listWatchlist"];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  "/api/v2/watchlist/{item_id}": {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** Answer whether the item is on the acting profile's watchlist: the entry, or 404 when it is not (or the viewer may not see it). */
    get: operations["getWatchlistEntry"];
    /** Add the item to the acting profile's watchlist. Automatic retries are unsafe because provider and refresh effects are not change-gated. */
    put: operations["addToWatchlist"];
    post?: never;
    /** Remove the item from the acting profile's watchlist; an absent entry succeeds, but automatic retries can repeat provider and refresh effects. */
    delete: operations["deleteWatchlistEntry"];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
}
export type webhooks = Record<string, never>;
export interface components {
  schemas: {
    Account: {
      /**
       * @description Whether the effective policy permits downloads
       * @example true
       */
      download_allowed: boolean;
      /**
       * @description Contact email; empty when none is set
       * @example alice@example.test
       */
      email: string;
      /**
       * @description Account identifier
       * @example 1
       */
      id: string;
      /** @description Present only while an administrator impersonates this account */
      impersonation?: components["schemas"]["Impersonation"];
      /**
       * @description Effective assignable permissions; empty for a disabled account
       * @example [
       *       "marker_edit"
       *     ]
       */
      permissions: string[];
      /**
       * @description Server-wide role
       * @example user
       * @enum {string}
       */
      role: "admin" | "user";
      /**
       * @description Login name
       * @example alice
       */
      username: string;
    };
    AdminJob: {
      cancelable: boolean;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       */
      created_at: string;
      deletion_result?: components["schemas"]["LibraryDeletionJobResult"];
      failure?: components["schemas"]["JobFailure"];
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       */
      finished_at?: string;
      id: string;
      kind: string;
      progress?: components["schemas"]["JobProgress"];
      refresh_result?: components["schemas"]["LibraryRefreshJobResult"];
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       */
      started_at?: string;
      state: string;
      terminal: boolean;
    };
    AdminUser: {
      /**
       * @description The access group the account belongs to; null when none
       * @example 2
       */
      access_group_id: string | null;
      /**
       * @description Override; null inherits
       * @example false
       */
      audio_transcode_allowed: boolean | null;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      created_at: string;
      /**
       * @description Override; null inherits
       * @example true
       */
      download_allowed: boolean | null;
      /**
       * @description Override; null inherits
       * @example false
       */
      download_transcode_allowed: boolean | null;
      /** @description The resolved policy the server enforces */
      effective_policy: components["schemas"]["EffectivePolicy"];
      /**
       * @description Contact email; empty when none is set
       * @example alice@example.test
       */
      email: string;
      /** @example true */
      enabled: boolean;
      /**
       * @description Opaque identifier
       * @example 1
       */
      id: string;
      /**
       * Format: date-time
       * @description Most recent recorded activity; null when the account has none
       * @example 2026-01-02T03:04:05.678Z
       */
      last_active_at: string | null;
      /**
       * @description Explicit library allowlist; null inherits the group's, empty means none
       * @example [
       *       "1",
       *       "2"
       *     ]
       */
      library_ids: string[] | null;
      /**
       * @description Playback ceiling override; null inherits, empty string means no ceiling
       * @example 1080p
       */
      max_playback_quality: string | null;
      /**
       * Format: int64
       * @description Household profile limit
       * @example 5
       */
      max_profiles: number;
      /**
       * Format: int64
       * @description Stream limit override; null inherits, 0 means unlimited
       * @example 2
       */
      max_streams: number | null;
      /**
       * Format: int64
       * @description Transcode limit override; null inherits, 0 means unlimited
       * @example 0
       */
      max_transcodes: number | null;
      /**
       * @description Permissions assigned directly to the account
       * @example [
       *       "marker_edit"
       *     ]
       */
      permissions: string[];
      /**
       * @description Override; null inherits
       * @example false
       */
      requests_allowed: boolean | null;
      /**
       * @example user
       * @enum {string}
       */
      role: "admin" | "user";
      /**
       * @description Override; null inherits
       * @example true
       */
      transcode_allowed: boolean | null;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      updated_at: string;
      /** @example alice */
      username: string;
    };
    AdminUserCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["AdminUser"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    AudioPreference: {
      /**
       * @description Preferred audio language; empty means no language preference
       * @example eng
       */
      audio_language: string;
      /**
       * Format: int64
       * @description Zero-based index of the chosen track, or -1 for no track
       * @example 1
       */
      audio_track_index: number;
      /**
       * @description Opaque identifier
       * @example p-1
       */
      profile_id: string;
      /**
       * @description The series content id
       * @example series-8f2c1a
       */
      series_id: string;
      /** @description Absent when the client stored none */
      track_signature?: components["schemas"]["AudioTrackSignature"];
      /**
       * Format: date-time
       * @description Last update; Unix epoch when an older SQLite track preference has no recorded timestamp
       * @example 2026-01-02T03:04:05.000Z
       */
      updated_at: string;
    };
    AudioPreferenceUpdate: {
      /**
       * @description Preferred audio language; empty or omitted means no language preference
       * @example eng
       */
      audio_language?: string;
      /**
       * Format: int64
       * @description Zero-based index of the chosen track, or -1 for no track
       * @example 1
       */
      audio_track_index: number;
      /** @description How playback recognizes the track when indexes shift */
      track_signature?: components["schemas"]["AudioTrackSignature"];
    };
    AudioTrackSignature: {
      /**
       * Format: int64
       * @example 6
       */
      channels?: number;
      /** @example eac3 */
      codec?: string;
      /**
       * @description Whether the track carried the default flag
       * @example true
       */
      default?: boolean;
      /**
       * @description Title embedded in the container
       * @example Surround
       */
      embedded_title?: string;
      /**
       * @description Track language (ISO 639)
       * @example eng
       */
      language?: string;
      /**
       * @description Channel layout
       * @example 5.1
       */
      layout?: string;
      /**
       * @description Display title the client showed
       * @example English 5.1
       */
      title?: string;
    };
    Calendar: {
      /** @description Empty, never null */
      events: components["schemas"]["CalendarDay"][];
    };
    CalendarDay: {
      /**
       * @description YYYY-MM-DD in the viewer's zone
       * @example 2026-01-10
       */
      date: string;
      /** @description Empty, never null */
      items: components["schemas"]["CalendarEvent"][];
    };
    CalendarEvent: {
      /**
       * Format: date-time
       * @description The airing as an instant, when the network's time and zone are known
       */
      air_at?: string;
      /**
       * @description The source air date, YYYY-MM-DD
       * @example 2026-01-09
       */
      air_date: string;
      /**
       * @description Wall-clock time in the network's zone, HH:MM
       * @example 21:00
       */
      air_time?: string;
      /**
       * @description The network's IANA zone
       * @example America/New_York
       */
      air_timezone?: string;
      /** @description series_premiere, season_premiere, finale; empty, never null */
      badges: string[];
      /** @example episode:severance-s02e01 */
      content_id: string;
      /**
       * Format: int64
       * @example 1
       */
      episode_number?: number;
      episode_title?: string;
      /**
       * @description The air date in the viewer's zone, YYYY-MM-DD; the day this event is listed under
       * @example 2026-01-10
       */
      local_air_date: string;
      poster_thumbhash?: string;
      /** @example https://s3.example.test/poster.jpg */
      poster_url?: string;
      /**
       * Format: int64
       * @example 2
       */
      season_number?: number;
      /** @example series:severance */
      series_id?: string;
      /**
       * @description The movie or series title
       * @example Severance
       */
      title: string;
      /**
       * @description movie, episode, or season_premiere
       * @example episode
       */
      type: string;
      /** @example false */
      watched: boolean;
    };
    CatalogItem: {
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       */
      added_at?: string;
      backdrop_thumbhash?: string;
      /** @description Presigned, short-lived */
      backdrop_url?: string;
      /** @description Section-specific badges (new, returning, …) */
      badges?: string[];
      /**
       * @description Deterministic catalog identifier
       * @example movie:heat-1995
       */
      content_id: string;
      /** @example R */
      content_rating?: string;
      /** Format: double */
      duration_seconds?: number;
      /** Format: int64 */
      episode_number?: number;
      /** @description Empty, never null */
      genres: string[];
      /** @description On a continue-watching card: in_progress or next_up */
      item_source?: string;
      /** @description Empty, never null */
      keywords: string[];
      /** @description Calendar date, YYYY-MM-DD */
      last_air_date?: string;
      /** @description Presigned, short-lived */
      logo_url?: string;
      /** Format: int64 */
      manga_chapter_count?: number;
      /** Format: int64 */
      manga_volume_count?: number;
      networks?: string[];
      /** @example en */
      original_language?: string;
      /** @description Technical badges of the best file */
      overlay_summary?: components["schemas"]["CatalogItemOverlay"];
      overview?: string;
      /** @description The item to play when the card is a series or season; absent when the item plays itself */
      play_content_id?: string;
      /**
       * Format: double
       * @description Resume position on a continue-watching card
       */
      position_seconds?: number;
      poster_thumbhash?: string;
      /** @description Presigned, short-lived */
      poster_url?: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       */
      progress_updated_at?: string;
      /** Format: double */
      rating_imdb?: number;
      /** Format: int64 */
      rating_rt_audience?: number;
      /** Format: int64 */
      rating_rt_critic?: number;
      /** Format: double */
      rating_tmdb?: number;
      /**
       * @description Calendar date, YYYY-MM-DD
       * @example 1995-12-15
       */
      release_date?: string;
      /**
       * Format: int64
       * @description Minutes
       * @example 170
       */
      runtime?: number;
      /** Format: int64 */
      season_number?: number;
      /** @description Owning series of an episode or season */
      series_id?: string;
      series_title?: string;
      /** @description Airing state of a series */
      show_status?: string;
      /** @description The values the listing was sorted by */
      sort_metrics?: components["schemas"]["CatalogItemSortMetrics"];
      /**
       * @description Metadata match state of the item
       * @example matched
       */
      status: string;
      studios?: string[];
      /** @example Heat */
      title: string;
      /**
       * @description movie, series, season, episode, audiobook, ebook, podcast, podcast_episode
       * @example movie
       */
      type: string;
      upcoming_event?: components["schemas"]["CatalogItemUpcomingEvent"];
      /** @description The viewer's flags; absent without a profile */
      user_state?: components["schemas"]["CatalogItemUserState"];
      /** @description Sibling editions of the same work */
      work_formats?: components["schemas"]["CatalogWorkFormat"][];
      /** @description The work (book) an audiobook or ebook edition belongs to */
      work_id?: string;
      work_title?: string;
      /**
       * Format: int64
       * @example 1995
       */
      year?: number;
    };
    CatalogItemCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["CatalogItem"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    CatalogItemOverlay: {
      /** @example 2.39:1 */
      aspect_ratio?: string;
      /** @example TrueHD */
      audio?: string;
      /** @example 7.1 */
      audio_channels?: string;
      /** @example MKV */
      container?: string;
      /** @example Director's Cut */
      edition?: string;
      /** @example Dolby Vision */
      hdr?: string;
      /** @description Two or more audio languages */
      multi_audio?: boolean;
      /** @description At least one subtitle track */
      multi_sub?: boolean;
      release_type?: string;
      /** @example 4K */
      resolution?: string;
      /** @example H.265 */
      video_codec?: string;
    };
    CatalogItemSortMetrics: {
      author?: string;
      /** Format: int64 */
      bitrate_kbps?: number;
      narrator?: string;
      /** Format: int64 */
      play_count?: number;
      /** Format: double */
      progress_ratio?: number;
      /** @description Calendar date, YYYY-MM-DD */
      release_date?: string;
      resolution?: string;
      /** Format: int64 */
      runtime_minutes?: number;
      series_name?: string;
      /** @description As the progress store recorded it */
      viewed_at?: string;
    };
    CatalogItemUpcomingEvent: {
      /**
       * @description Calendar date, YYYY-MM-DD
       * @example 2026-01-09
       */
      air_date: string;
      /** @description Local wall-clock time, HH:MM */
      air_time?: string;
      /** @description Empty, never null */
      badges: string[];
      /** Format: int64 */
      episode_number?: number;
      episode_title?: string;
      /** Format: int64 */
      season_number?: number;
      /**
       * @description premiere, episode, finale, …
       * @example episode
       */
      type: string;
    };
    CatalogItemUserState: {
      /** @example false */
      in_watchlist: boolean;
      /** @example false */
      is_favorite: boolean;
      /** @example false */
      played: boolean;
    };
    CatalogWorkFormat: {
      /** @example audiobook:dune */
      content_id: string;
      /**
       * @description Absent when the edition is not in a library
       * @example 1
       */
      library_id?: string;
      /** @example audiobook */
      type: string;
    };
    CuratedCollection: {
      backdrop_thumbhash?: string;
      /** @description Presigned, short-lived; empty when none */
      backdrop_url: string;
      /**
       * @description manual, smart, or an import type
       * @example manual
       */
      collection_type: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      created_at: string;
      description: string;
      /** @example false */
      featured: boolean;
      /** @description null when ungrouped */
      group_id: string | null;
      /** @example 01J9Z8C3W4R5T6Y7U8I9O0P1Q2 */
      id: string;
      /**
       * Format: int64
       * @example 12
       */
      item_count: number;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       */
      last_sync_at?: string;
      last_sync_message: string;
      last_sync_status: string;
      /**
       * @description Opaque identifier
       * @example 1
       */
      library_id: string;
      /** @description Every library the collection spans; empty, never null */
      library_ids: string[];
      management_key: string;
      /** @example manual */
      management_mode: string;
      management_source: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       */
      next_sync_at?: string;
      poster_thumbhash?: string;
      /** @description Presigned, short-lived; empty when none */
      poster_url: string;
      /** @description Smart collection query; null when not a smart collection */
      query_definition: unknown;
      /** @example oscar-winners */
      slug: string;
      /** @description Sort configuration document; null when default */
      sort_config: unknown;
      /**
       * Format: int64
       * @example 0
       */
      sort_order: number;
      /** @description Import source configuration; null when not imported */
      source_config: unknown;
      /** @description Where an imported collection came from; empty otherwise */
      source_url: string;
      sync_schedule?: string;
      /** @example Oscar Winners */
      title: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      updated_at: string;
      /** @example visible */
      visibility: string;
    };
    EffectivePolicy: {
      /** @example false */
      audio_transcode_allowed: boolean;
      /** @example true */
      download_allowed: boolean;
      /** @example false */
      download_transcode_allowed: boolean;
      /**
       * @description Libraries the account may see; empty means every library
       * @example [
       *       "1",
       *       "2"
       *     ]
       */
      library_ids: string[];
      /**
       * @description Playback ceiling; empty means none
       * @example 1080p
       */
      max_playback_quality: string;
      /**
       * Format: int64
       * @description Concurrent stream limit; 0 means unlimited
       * @example 2
       */
      max_streams: number;
      /**
       * Format: int64
       * @description Concurrent transcode limit; 0 means unlimited
       * @example 0
       */
      max_transcodes: number;
      /**
       * @description Effective assignable permissions
       * @example [
       *       "marker_edit"
       *     ]
       */
      permissions: string[];
      /** @example false */
      requests_allowed: boolean;
      /** @example true */
      transcode_allowed: boolean;
    };
    EffectiveSettingCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["EffectiveSettingValue"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
      /**
       * Format: int64
       * @description The settings contract revision this server serves
       * @example 8
       */
      revision: number;
    };
    EffectiveSettingContext: {
      /**
       * @description The context_id of the request entry this answers
       * @example row-1
       */
      context_id: string;
      /** @description The requested keys resolved under this context, in request order */
      settings: components["schemas"]["EffectiveSettingValue"][];
    };
    EffectiveSettingContextCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["EffectiveSettingContext"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
      /**
       * Format: int64
       * @description The settings contract revision this server serves
       * @example 8
       */
      revision: number;
    };
    EffectiveSettingsBatch: {
      /** @description The content contexts to resolve under; each context may name a library and a series */
      contexts: components["schemas"]["SettingContextRequest"][];
      /**
       * @description The setting keys to resolve under every context
       * @example [
       *       "playback.preferred_quality"
       *     ]
       */
      keys: string[];
    };
    EffectiveSettingValue: {
      /**
       * @description The client family of the winning row
       * @example tv
       */
      client_family?: string;
      /**
       * @description Whether policy narrowed the authored value
       * @example false
       */
      constrained?: boolean;
      /** @description The policy input that narrowed it; absent when unconstrained */
      constrained_by?: components["schemas"]["SettingConstraint"];
      /**
       * @description How policy narrowed it; absent when unconstrained
       * @example ceiling
       */
      constraint_kind?: string;
      /**
       * Format: int64
       * @description The contract revision that last changed this key's definition
       * @example 3
       */
      definition_revision: number;
      /**
       * @description The device of the winning row
       * @example iphone-1
       */
      device_id?: string;
      /**
       * @description The setting key
       * @example ui.theme
       */
      key: string;
      /**
       * @description The library of the winning row
       * @example 3
       */
      library_id?: string;
      /** @description The values policy still allows; absent when unconstrained */
      permitted_values?: unknown[];
      /**
       * @description The profile of the winning row
       * @example 1
       */
      profile_id?: string;
      /** @description The value the profile asked for before the constraint; absent when unconstrained */
      requested_value?: unknown;
      /**
       * @description The scope of the winning row, so a client can reset exactly that scope; absent for a default
       * @example profile
       */
      scope?: string;
      /**
       * @description The series of the winning row
       * @example tv:12345
       */
      series_id?: string;
      /**
       * @description The scope the value came from, or default for the contract default
       * @example profile
       */
      source: string;
      /** @description The identity of the winning stored row (the members scope through series_id, nested); absent for a default. Not the content context a batched resolve was asked for */
      source_context?: components["schemas"]["SettingSourceContext"];
      /** @description The authored value when policy narrowed it; absent otherwise */
      stored_value?: unknown;
      /**
       * @description Advisory suggestions for an open setting; never a write allowlist
       * @example [
       *       "en",
       *       "fr"
       *     ]
       */
      suggested_values?: string[];
      /**
       * Format: date-time
       * @description When the winning stored row was last written; absent for a default
       * @example 2026-01-02T03:04:05.000Z
       */
      updated_at?: string;
      /** @description The value that applies after resolution and policy */
      value: unknown;
    };
    EffectiveSubtitleAppearance: {
      /**
       * @description The device the override belongs to; absent when there is none
       * @example iphone-1
       */
      device_id?: string;
      /**
       * @description The name the device registered with; absent when unknown
       * @example Living room
       */
      device_name?: string;
      /**
       * @description The platform the device registered with; absent when unknown
       * @example iOS
       */
      device_platform?: string;
      /**
       * @description The device override as a JSON document; absent when the device has none
       * @example {"fontSize":"xxlarge"}
       */
      device_value?: string;
      /**
       * @description The value that applies on this device; empty when nothing is set
       * @example {"fontSize":"xxlarge"}
       */
      effective_value: string;
      /**
       * @description The profile-wide value as a JSON document; empty when unset
       * @example {"fontSize":"large"}
       */
      global_value: string;
      /**
       * @description Whether device_value is what applies
       * @example true
       */
      has_device_override: boolean;
      /**
       * @description Always subtitle_appearance
       * @example subtitle_appearance
       */
      key: string;
      /**
       * @description The acting profile
       * @example 1
       */
      profile_id: string;
      /**
       * Format: date-time
       * @description When the device override was last written; absent when there is none
       * @example 2026-01-02T03:04:05.000Z
       */
      updated_at?: string;
    };
    EmptyRootCleanup: {
      /** @example Empty-root cleanup confirmed for next scan */
      message: string;
      /** @example ok */
      status: string;
    };
    ExplicitSettingValue: {
      /**
       * @description The client family of a profile_client scope
       * @example tv
       */
      client_family?: string;
      /**
       * @description The device of a profile_device scope
       * @example iphone-1
       */
      device_id?: string;
      /**
       * @description Whether a value is stored at this scope
       * @example true
       */
      is_set: boolean;
      /**
       * @description The setting key
       * @example ui.theme
       */
      key: string;
      /**
       * @description The library of a profile_library scope
       * @example 3
       */
      library_id?: string;
      /**
       * @description The profile the scope belongs to; absent at account scope
       * @example 1
       */
      profile_id?: string;
      /**
       * Format: int64
       * @description The row's revision; absent when is_set is false
       * @example 4
       */
      revision?: number;
      /**
       * @description The scope that was read
       * @example profile
       */
      scope: string;
      /**
       * @description The series of a profile_series scope
       * @example tv:12345
       */
      series_id?: string;
      /**
       * Format: date-time
       * @description When the value was last written; absent when unset or unrecorded
       * @example 2026-01-02T03:04:05.000Z
       */
      updated_at?: string;
      /** @description The stored value; absent when is_set is false */
      value?: unknown;
    };
    FavoriteCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["CatalogItem"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    FavoriteEntry: {
      /**
       * Format: date-time
       * @description When the item became a favorite
       * @example 2026-01-02T03:04:05.000Z
       */
      added_at: string;
      /**
       * @description The catalog item
       * @example movie:heat-1995
       */
      item_id: string;
    };
    FormFile: {
      ContentType: string;
      Filename: string;
      IsSet: boolean;
      /** Format: int64 */
      Size: number;
    };
    HistoryCard: {
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       */
      added_at?: string;
      backdrop_thumbhash?: string;
      /** @description Presigned, short-lived */
      backdrop_url?: string;
      /** @description Section-specific badges (new, returning, …) */
      badges?: string[];
      /**
       * @description Deterministic catalog identifier
       * @example movie:heat-1995
       */
      content_id: string;
      /** @example R */
      content_rating?: string;
      /** Format: double */
      duration_seconds?: number;
      /** Format: int64 */
      episode_number?: number;
      /** @description Empty, never null */
      genres: string[];
      /** @description On a continue-watching card: in_progress or next_up */
      item_source?: string;
      /** @description Empty, never null */
      keywords: string[];
      /** @description Calendar date, YYYY-MM-DD */
      last_air_date?: string;
      /** @description Presigned, short-lived */
      logo_url?: string;
      /** Format: int64 */
      manga_chapter_count?: number;
      /** Format: int64 */
      manga_volume_count?: number;
      networks?: string[];
      /** @example en */
      original_language?: string;
      /** @description Technical badges of the best file */
      overlay_summary?: components["schemas"]["CatalogItemOverlay"];
      overview?: string;
      /** @description The item to play when the card is a series or season; absent when the item plays itself */
      play_content_id?: string;
      /**
       * Format: double
       * @description Resume position on a continue-watching card
       */
      position_seconds?: number;
      poster_thumbhash?: string;
      /** @description Presigned, short-lived */
      poster_url?: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       */
      progress_updated_at?: string;
      /** Format: double */
      rating_imdb?: number;
      /** Format: int64 */
      rating_rt_audience?: number;
      /** Format: int64 */
      rating_rt_critic?: number;
      /** Format: double */
      rating_tmdb?: number;
      /**
       * @description Calendar date, YYYY-MM-DD
       * @example 1995-12-15
       */
      release_date?: string;
      /**
       * Format: int64
       * @description Minutes
       * @example 170
       */
      runtime?: number;
      /** Format: int64 */
      season_number?: number;
      /** @description Owning series of an episode or season */
      series_id?: string;
      series_title?: string;
      /** @description Airing state of a series */
      show_status?: string;
      /** @description The values the listing was sorted by */
      sort_metrics?: components["schemas"]["CatalogItemSortMetrics"];
      /**
       * @description Metadata match state of the item
       * @example matched
       */
      status: string;
      studios?: string[];
      /** @example Heat */
      title: string;
      /**
       * @description movie, series, season, episode, audiobook, ebook, podcast, podcast_episode
       * @example movie
       */
      type: string;
      upcoming_event?: components["schemas"]["CatalogItemUpcomingEvent"];
      /** @description The viewer's flags; absent without a profile */
      user_state?: components["schemas"]["CatalogItemUserState"];
      /** @description The most recent watch of the card's item */
      watch: components["schemas"]["HistoryWatch"];
      /** @description Sibling editions of the same work */
      work_formats?: components["schemas"]["CatalogWorkFormat"][];
      /** @description The work (book) an audiobook or ebook edition belongs to */
      work_id?: string;
      work_title?: string;
      /**
       * Format: int64
       * @example 1995
       */
      year?: number;
    };
    HistoryCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["HistoryCard"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    HistoryRemovalTarget: {
      /**
       * @description A movie, ebook, series, season or episode
       * @example episode:heat-s01e01
       */
      content_id: string;
      /**
       * @description item (default) removes the target itself, a series or season expanding to its episodes; show widens a season or episode to its whole series
       * @example item
       * @enum {string}
       */
      scope?: "item" | "show";
    };
    HistoryRemoveInputBody: {
      /** @description Targets to hide from history */
      targets: components["schemas"]["HistoryRemovalTarget"][];
    };
    HistoryWatch: {
      /**
       * @description Whether the watch counted as finished
       * @example true
       */
      completed: boolean;
      /**
       * Format: double
       * @description Known runtime at the time; 0 when unknown
       * @example 5400
       */
      duration_seconds: number;
      /**
       * @description The item that was watched; an episode when the card is its series
       * @example episode:heat-s01e01
       */
      media_item_id: string;
      /**
       * @description How the watch was recorded: playback, manual, import, legacy
       * @example playback
       */
      source?: string;
      /**
       * Format: date-time
       * @description When the watch was recorded
       * @example 2026-01-02T03:04:05.000Z
       */
      watched_at: string;
    };
    HomeDismissal: {
      /**
       * Format: date-time
       * @description Required for continue_watching: the card's progress_updated_at; the dismissal holds until the item is played again
       */
      progress_updated_at?: string;
      /**
       * @description Required for next_up: the series the episode card belongs to
       * @example series:severance
       */
      series_id?: string;
    };
    Impersonation: {
      /**
       * @description Always true when the object is present
       * @example true
       */
      active: boolean;
      /**
       * @description The administrator account acting as this account
       * @example 7
       */
      impersonator_user_id: string;
      /**
       * @description The administrator's username; empty when the account no longer exists
       * @example alice
       */
      impersonator_username: string;
    };
    JobFailure: {
      detail: string;
      retryable: boolean;
      title: string;
      type: string;
    };
    JobProgress: {
      /** Format: int64 */
      current: number;
      /** Format: int64 */
      total: number;
      unit: string;
    };
    Library: {
      /**
       * @description Translate descriptions when providers lack the language
       * @example false
       */
      auto_translate_metadata: boolean;
      /** @example false */
      chapter_thumbnails_enabled: boolean;
      /**
       * @description Whether the server can produce chapter thumbnails (public asset storage is configured)
       * @example true
       */
      chapter_thumbnails_supported: boolean;
      /** @example true */
      enabled: boolean;
      /**
       * @description Opaque identifier
       * @example 1
       */
      id: string;
      /** @example false */
      intro_detection_enabled: boolean;
      /**
       * Format: date-time
       * @description Absent until the first scan completes
       */
      last_scanned_at?: string;
      /**
       * @description ISO 639-1 code metadata is fetched in
       * @example en
       */
      metadata_language: string;
      /** @example Movies */
      name: string;
      /**
       * @description Root directories the library scans
       * @example [
       *       "/media/movies"
       *     ]
       */
      paths: string[];
      /** @description Presigned poster URL; absent when the library has no poster */
      poster_url?: string;
      /**
       * Format: date-time
       * @description When the warning was raised; absent when none
       */
      scan_warning_at?: string;
      /**
       * @description Outstanding scan warning (empty_root, dead_root, …); absent when none
       * @example empty_root
       */
      scan_warning_code?: string;
      /** @description Human-readable warning; absent when none */
      scan_warning_message?: string;
      /**
       * Format: int64
       * @description Position among libraries, lowest first
       * @example 0
       */
      sort_order: number;
      /**
       * @description Remote video kinds fetched during metadata refresh; empty disables them
       * @example [
       *       "trailer"
       *     ]
       */
      trailer_kinds: string[];
      /**
       * @description Library kind (movies, series, mixed, audiobooks, ebooks, podcasts, manga); free-form until the vocabulary is ratified (#135)
       * @example movies
       */
      type: string;
    };
    LibraryCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["Library"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    LibraryCollectionCard: {
      /** @description Present on a personal collection: the profile that made it */
      creator_profile_id?: string;
      featured?: boolean;
      /** @example 01J9Z8C3W4R5T6Y7U8I9O0P1Q2 */
      id: string;
      /**
       * Format: int64
       * @example 12
       */
      item_count: number;
      poster_thumbhash?: string;
      /** @description Presigned, short-lived; empty when none */
      poster_url: string;
      /** @example Oscar Winners */
      title: string;
    };
    LibraryCollectionGroup: {
      collections: components["schemas"]["LibraryCollectionCard"][];
      /** @example 01J9Z8C3W4R5T6Y7U8I9O0P1Q3 */
      id: string;
      /**
       * @description admin or user_collections
       * @example admin
       */
      kind: string;
      /** @example Franchises */
      name: string;
      /** @example name_asc */
      sort_mode: string;
      /**
       * Format: int64
       * @example 0
       */
      sort_order: number;
    };
    LibraryCollectionTab: {
      /** @description Every visible curated collection in full; empty, never null */
      collections: components["schemas"]["CuratedCollection"][];
      /** @description Non-empty groups in display order; empty when groups are not configured */
      groups: components["schemas"]["LibraryCollectionGroup"][];
      /**
       * @description Opaque identifier
       * @example 1
       */
      library_id: string;
      /** @description Absent when every collection is grouped */
      ungrouped?: components["schemas"]["LibraryCollectionUngrouped"];
    };
    LibraryCollectionUngrouped: {
      collections: components["schemas"]["LibraryCollectionCard"][];
      /**
       * Format: int64
       * @example 9999
       */
      sort_order: number;
    };
    LibraryCreate: {
      /**
       * @description Requires public asset storage
       * @example false
       */
      chapter_thumbnails_enabled?: boolean;
      /** @example false */
      intro_detection_enabled?: boolean;
      /**
       * @description ISO 639-1 code; default en
       * @example en
       */
      metadata_language?: string;
      /** @example Movies */
      name: string;
      /**
       * @description Root directories the library scans
       * @example [
       *       "/media/movies"
       *     ]
       */
      paths: string[];
      /**
       * @description Remote video kinds to fetch; omitted applies the default (every provider kind), empty disables them
       * @example [
       *       "trailer"
       *     ]
       */
      trailer_kinds?: string[];
      /**
       * @description Library kind (movies, series, mixed, audiobooks, ebooks, podcasts, manga)
       * @example movies
       */
      type: string;
    };
    LibraryDeletionJobResult: {
      /** Format: int64 */
      deleted_item_links: number;
      /** Format: int64 */
      deleted_media_files: number;
      /** Format: int64 */
      deleted_orphaned_items: number;
      /**
       * @description Opaque identifier
       * @example 1
       */
      library_id: string;
    };
    LibraryMountCheck: {
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      checked_at: string;
      /** @example true */
      healthy: boolean;
      /**
       * @description Opaque identifier
       * @example 1
       */
      library_id: string;
      /** @example Movies */
      library_name: string;
      roots: components["schemas"]["LibraryMountCheckRoot"][];
      /**
       * @description Overall verdict
       * @example healthy
       */
      status: string;
      /** @example All 1 roots reachable */
      summary: string;
    };
    LibraryMountCheckRoot: {
      /**
       * @description Why the root is unreachable; null when it is
       * @example not_found
       */
      error_code: string | null;
      /** @description Human-readable reason; null when reachable */
      error_message: string | null;
      /** @example /media/movies */
      path: string;
      /** @example true */
      reachable: boolean;
      /**
       * @description Reachable but every known file under it is missing: the signature of a lost mount exposing an empty mountpoint
       * @example false
       */
      suspect_empty: boolean;
    };
    LibraryOrderEntry: {
      /**
       * @description Opaque identifier
       * @example 1
       */
      id: string;
      /**
       * Format: int64
       * @example 0
       */
      position: number;
    };
    LibraryPlaybackPreference: {
      /**
       * @description Audio language override (BCP 47 language tag); empty means none
       * @example en
       */
      audio_language?: string;
      /**
       * @description Opaque identifier
       * @example 1
       */
      library_id: string;
      /**
       * @description Opaque identifier
       * @example p-1
       */
      profile_id: string;
      /** @example false */
      show_forced_subtitles?: boolean;
      /**
       * @description Subtitle language override (BCP 47 language tag); empty means none
       * @example en
       */
      subtitle_language?: string;
      /**
       * @description Subtitle mode override. Canonical values: auto, always, off; empty means none
       * @example auto
       */
      subtitle_mode?: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.000Z
       */
      updated_at: string;
    };
    LibraryPlaybackPreferenceCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["LibraryPlaybackPreference"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    LibraryPlaybackPreferenceUpdate: {
      /**
       * @description Audio language override (BCP 47 language tag); null or empty clears it
       * @example en
       */
      audio_language?: string | null;
      /**
       * @description Forced-subtitle override; null clears it
       * @example true
       */
      show_forced_subtitles?: boolean | null;
      /**
       * @description Subtitle language override (BCP 47 language tag); null or empty clears it
       * @example en
       */
      subtitle_language?: string | null;
      /**
       * @description Subtitle mode override: auto, always or off; null or empty clears it
       * @example always
       */
      subtitle_mode?: string | null;
    };
    LibraryPosterForm: {
      /**
       * Format: binary
       * @description The image file: image/jpeg, image/png or image/webp, at most 10 MiB
       */
      poster: string;
    };
    LibraryProviderDefaults: {
      /** @description One entry per content level the type has, in level-name order; empty for a type the server seeds no chain for */
      levels: components["schemas"]["ProviderChainLevel"][];
    };
    LibraryProviders: {
      /** @description One entry per content level with a chain, in level-name order */
      levels: components["schemas"]["ProviderChainLevel"][];
    };
    LibraryProvidersSet: {
      levels: components["schemas"]["ProviderChainLevelInput"][];
    };
    LibraryRefresh: {
      /**
       * @description quick refreshes stale items only; full refreshes every item. Default quick
       * @example quick
       * @enum {string}
       */
      mode?: "quick" | "full";
    };
    LibraryRefreshJobResult: {
      /** Format: int64 */
      failed: number;
      /**
       * @description Opaque identifier
       * @example 1
       */
      library_id: string;
      /** Format: int64 */
      refreshed: number;
    };
    LibraryReorder: {
      /** @description Libraries and their positions; libraries not named keep their order after the named ones */
      entries: components["schemas"]["LibraryOrderEntry"][];
    };
    LibraryRoot: {
      /** @description Absent when no operator override applies */
      active_override?: components["schemas"]["RootOverride"];
      /** @description The catalog item this root matched; absent when unmatched */
      content_id?: string;
      /** @description The scanner's inference evidence, as recorded */
      evidence_json?: unknown;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      first_seen_at: string;
      /** @example tt0113277 */
      imdb_id?: string;
      /** @example movie */
      inferred_type: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      last_seen_at: string;
      /**
       * @description Opaque identifier
       * @example 1
       */
      library_id: string;
      /** @example Movies */
      library_name: string;
      /**
       * Format: int64
       * @example 1
       */
      observed_file_count: number;
      /** @description Where the active identity came from when overridden */
      override_source?: string;
      /** @example /media/movies/Heat (1995) */
      root_path: string;
      /** @example /media/movies/Heat (1995)/Heat.mkv */
      sample_file_path?: string;
      /**
       * @description Inference state of the root
       * @example resolved
       */
      state: string;
      /** @example Heat */
      title: string;
      /** @example 949 */
      tmdb_id?: string;
      tvdb_id?: string;
      /** @example high */
      type_confidence: string;
      /**
       * Format: int64
       * @description 0 when unknown
       * @example 1995
       */
      year: number;
    };
    LibraryRootCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["LibraryRoot"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
      /**
       * Format: int64
       * @description Roots matching the filter across every page
       * @example 1
       */
      total: number;
    };
    LibraryUpdate: {
      /** @example false */
      auto_translate_metadata?: boolean;
      /** @example false */
      chapter_thumbnails_enabled?: boolean;
      /** @example true */
      enabled?: boolean;
      /** @example false */
      intro_detection_enabled?: boolean;
      /**
       * @description ISO 639-1 code; a change queues a quick metadata refresh
       * @example en
       */
      metadata_language?: string;
      /** @example Movies */
      name?: string;
      /**
       * @description Replaces every root; a changed set queues a rescan
       * @example [
       *       "/media/movies"
       *     ]
       */
      paths?: string[];
      /**
       * @description Replaces the allow-list; empty disables remote videos
       * @example [
       *       "trailer"
       *     ]
       */
      trailer_kinds?: string[];
      /** @example movies */
      type?: string;
    };
    MetadataMatchQueueAction: {
      /**
       * @description Opaque identifier
       * @example 1
       */
      library_id: string;
      /** Format: int64 */
      movie_cancelled?: number;
      /** @description The counts afterwards */
      queue: components["schemas"]["MetadataMatchQueueStatus"];
      /** Format: int64 */
      raw_file_cancelled?: number;
      /** Format: int64 */
      raw_file_retried?: number;
      /** Format: int64 */
      series_cancelled?: number;
      /**
       * @description queued after a retry, cancelled after a cancel
       * @example queued
       */
      status: string;
      /** Format: int64 */
      total_cancelled?: number;
    };
    MetadataMatchQueueDetail: {
      /**
       * @description Opaque identifier
       * @example 1
       */
      library_id: string;
      /**
       * Format: int64
       * @example 0
       */
      movie_count: number;
      /** @description Empty, never null */
      movies: components["schemas"]["MovieMatchQueueEntry"][];
      /** @description The three lists page together */
      page: components["schemas"]["PageInfo"];
      /**
       * Format: int64
       * @description Entries the matcher gave up on until an operator retries them
       * @example 0
       */
      parked_count: number;
      /**
       * Format: int64
       * @example 0
       */
      pending_count: number;
      /**
       * Format: int64
       * @example 0
       */
      raw_file_count: number;
      /** @description Empty, never null */
      raw_files: components["schemas"]["RawMatchBacklogEntry"][];
      /** @description Empty, never null */
      series: components["schemas"]["SeriesMatchQueueEntry"][];
      /**
       * Format: int64
       * @example 0
       */
      series_count: number;
      /**
       * Format: int64
       * @example 0
       */
      total_count: number;
    };
    MetadataMatchQueueStatus: {
      /**
       * @description Opaque identifier
       * @example 1
       */
      library_id: string;
      /**
       * Format: int64
       * @example 0
       */
      movie_count: number;
      /**
       * Format: int64
       * @description Entries the matcher gave up on until an operator retries them
       * @example 0
       */
      parked_count: number;
      /**
       * Format: int64
       * @example 0
       */
      pending_count: number;
      /**
       * Format: int64
       * @example 0
       */
      raw_file_count: number;
      /**
       * Format: int64
       * @example 0
       */
      series_count: number;
      /**
       * Format: int64
       * @example 0
       */
      total_count: number;
    };
    MetadataMatchQueueStatusCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["MetadataMatchQueueStatus"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    MovieMatchQueueEntry: {
      /**
       * Format: int64
       * @example 1
       */
      attempt_count: number;
      /**
       * Format: date-time
       * @description When the matcher may next try
       * @example 2026-01-02T03:04:05.678Z
       */
      available_at: string;
      /**
       * Format: int64
       * @example 0
       */
      deterministic_attempt_count: number;
      /** @description Matcher-specific failure document */
      failure_detail?: unknown;
      failure_kind?: string;
      /** @example /media/movies/Heat (1995)/Heat.mkv */
      file_path: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      first_queued_at: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       */
      last_attempted_at?: string;
      last_error?: string;
      /**
       * @description Opaque identifier
       * @example 1
       */
      library_id: string;
      /**
       * Format: int64
       * @example 3
       */
      matcher_revision: number;
      /**
       * @description Opaque identifier
       * @example 120
       */
      media_file_id: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       */
      parked_at?: string;
      /**
       * @description pending or parked
       * @example pending
       */
      state: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      updated_at: string;
    };
    NavigationShortcutMutation: {
      /** @description The shortcut to add or remove; its destination identity, not its label, decides which entry it is */
      item: {
        [key: string]: unknown;
      };
      /**
       * @description true adds or relabels the shortcut, false removes it
       * @example true
       */
      present: boolean;
    };
    OverlayConfig: {
      /**
       * @description Administrator-chosen overlay defaults document; absent when none is set
       * @example {"badges":true}
       */
      defaults?: string;
      /**
       * @description Whether card overlays are enabled server-wide
       * @example true
       */
      enabled: boolean;
      /**
       * @description Default quick-action mode; one of the ui.card_quick_actions values in the settings contract
       * @example both
       */
      quick_actions_default: string;
      /**
       * @description Default for profiles that have not chosen whether cards show quick actions
       * @example false
       */
      quick_actions_enabled: boolean;
    };
    PageInfo: {
      /**
       * @description Whether a next page exists
       * @example true
       */
      has_more: boolean;
      /**
       * @description Opaque cursor for the next page; absent on the last page
       * @example eyJvZmZzZXQiOjUwfQ
       */
      next_cursor?: string;
    };
    PlaybackSession: {
      /**
       * @description Empty when unknown
       * @example copy
       */
      audio_decision: string;
      /**
       * Format: int64
       * @example 0
       */
      audio_track_index: number;
      /**
       * @description Empty when unknown
       * @example 1400
       */
      client_build: string;
      /**
       * @description Empty when unknown
       * @example release
       */
      client_channel: string;
      /**
       * @description Empty when unknown
       * @example 192.0.2.10
       */
      client_ip: string;
      /**
       * @description Display label derived from the client name and version; empty when unknown
       * @example Silo for Apple TV 1.4
       */
      client_label: string;
      /**
       * @description Display label with the exact build; empty when unknown
       * @example Silo for Apple TV 1.4.0 (1400)
       */
      client_label_full: string;
      /**
       * @description Empty when unknown
       * @example Silo for Apple TV
       */
      client_name: string;
      /**
       * @description Empty when unknown
       * @example
       */
      client_user_agent: string;
      /**
       * @description Empty when unknown
       * @example 1.4.0
       */
      client_version: string;
      /**
       * @description Catalog content id of the playing item; empty when unknown
       * @example tt0111161
       */
      content_id: string;
      /**
       * @description Bucketed method: direct, remux, transcode or audio; empty when unknown
       * @example direct
       */
      effective_play_method: string;
      /**
       * @description Empty unless an episode
       * @example
       */
      episode_name: string;
      /**
       * Format: int64
       * @description null unless an episode
       * @example 1
       */
      episode_number: number | null;
      /**
       * Format: int64
       * @description Seconds; null when unknown
       * @example 8520
       */
      file_duration: number | null;
      /**
       * @description Whether the serving node accepts remote control of this session
       * @example true
       */
      has_playback_control: boolean;
      /**
       * @description The playback session
       * @example ps_7f3a
       */
      id: string;
      /** @example false */
      is_jellyfin_client: boolean;
      /** @example false */
      is_paused: boolean;
      /**
       * @description Opaque identifier
       * @example 42
       */
      media_file_id: string;
      /** @example The Shawshank Redemption */
      media_title: string;
      /**
       * @description Catalog item type; empty when unknown
       * @example movie
       */
      media_type: string;
      /**
       * @description Empty when the node has no display name
       * @example
       */
      node_display_name: string;
      /**
       * @description The negotiated method as the node reported it
       * @example direct
       */
      play_method: string;
      /**
       * Format: double
       * @example 1234.5
       */
      position_seconds: number;
      /**
       * @description Where to fetch the poster; empty when there is none
       * @example /api/v1/images/poster/42
       */
      poster_url: string;
      /**
       * @description The profile playing; null when the session carries none
       * @example 1
       */
      profile_id: string | null;
      /**
       * @description Empty when unknown
       * @example Laura
       */
      profile_name: string;
      /**
       * @description Identifier of the node serving the stream
       * @example api
       */
      reporting_node: string;
      /**
       * @description The file the client asked for before any version substitution
       * @example 42
       */
      requested_media_file_id: string;
      /**
       * @description Empty when the client did not ask for one
       * @example
       */
      requested_video_codec: string;
      /**
       * @description Empty when the client did not ask for one
       * @example
       */
      requested_video_resolution: string;
      /**
       * @description Empty when routing is unresolved
       * @example
       */
      routing_egress: string;
      /**
       * @description null when routing is unresolved
       * @example 3
       */
      routing_egress_node_id: string | null;
      /**
       * @description Empty when routing is unresolved
       * @example
       */
      routing_egress_node_name: string;
      /**
       * @description Empty when routing is unresolved
       * @example
       */
      routing_execution: string;
      /**
       * @description null when routing is unresolved
       * @example 3
       */
      routing_execution_node_id: string | null;
      /**
       * @description Empty when routing is unresolved
       * @example
       */
      routing_execution_node_name: string;
      /**
       * @description Empty when routing is unresolved
       * @example
       */
      routing_workload: string;
      /**
       * Format: int64
       * @description null unless an episode
       * @example 1
       */
      season_number: number | null;
      /**
       * @description Empty unless an episode
       * @example
       */
      series_name: string;
      /**
       * Format: int64
       * @description null when unknown
       * @example 8
       */
      source_audio_channels: number | null;
      /**
       * @description Empty when unknown
       * @example truehd
       */
      source_audio_codec: string;
      /**
       * @description Empty when unknown
       * @example eng
       */
      source_audio_language: string;
      /**
       * @description Empty when unknown
       * @example 7.1
       */
      source_audio_layout: string;
      /**
       * @description Empty when unknown
       * @example
       */
      source_audio_title: string;
      /**
       * Format: int64
       * @description null when unknown
       * @example 24000
       */
      source_bitrate_kbps: number | null;
      /**
       * @description Empty when unknown
       * @example mkv
       */
      source_container: string;
      /**
       * @description Empty when unknown
       * @example hevc
       */
      source_video_codec: string;
      /**
       * @description Empty when unknown
       * @example 2160p
       */
      source_video_resolution: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.000Z
       */
      started_at: string;
      /**
       * Format: int64
       * @description null when unknown
       * @example 12000
       */
      stream_bitrate_kbps: number | null;
      /**
       * Format: int64
       * @description Channels the transcode encodes; null when the reporting node did not know
       * @example 6
       */
      target_audio_channels: number | null;
      /**
       * @description Empty when not transcoding
       * @example
       */
      target_audio_codec: string;
      /**
       * Format: int64
       * @description null when not transcoding
       * @example 8000
       */
      target_bitrate_kbps: number | null;
      /**
       * @description Empty when not transcoding
       * @example
       */
      target_resolution: string;
      /**
       * @description Empty when not transcoding
       * @example
       */
      target_video_codec: string;
      /**
       * @description Confirmed tone-map executor; empty when none
       * @example
       */
      tone_map_mode: string;
      /** @example false */
      transcode_audio: boolean;
      /**
       * @description Confirmed transcode executor; empty when not transcoding
       * @example
       */
      transcode_hw_accel: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.000Z
       */
      updated_at: string;
      /**
       * @description Opaque identifier
       * @example 1
       */
      user_id: string;
      /** @example laura */
      username: string;
      /**
       * @description Empty when unknown
       * @example copy
       */
      video_decision: string;
    };
    PlaybackSessionCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["PlaybackSession"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    PluginAsset: {
      /**
       * @description Media type
       * @example text/javascript
       */
      content_type: string;
      /**
       * @description Subresource integrity digest
       * @example sha256-...
       */
      integrity: string;
      /**
       * @description Asset path under the plugin's proxy prefix
       * @example /app.js
       */
      path: string;
    };
    PluginRoute: {
      /**
       * @description Who may call it, as the plugin declared
       * @example user
       */
      access: string;
      /**
       * @description Route identifier within the plugin
       * @example dashboard
       */
      id: string;
      /**
       * @description HTTP method
       * @example GET
       */
      method: string;
      /**
       * @description Whether clients list it in navigation
       * @example true
       */
      navigable: boolean;
      /**
       * @description Navigation surface: user or admin
       * @example user
       */
      navigation_kind: string;
      /**
       * @description Label for navigation; empty when not navigable
       * @example Dashboard
       */
      navigation_label: string;
      /**
       * @description Path under the plugin's proxy prefix
       * @example /dashboard
       */
      path: string;
      /**
       * @description Whether the route serves a packaged asset
       * @example false
       */
      static_asset: boolean;
    };
    PluginSettings: {
      /** @description The installation and what it asks for */
      installation: components["schemas"]["PluginSettingsInstallation"];
      /** @description The account's stored values; empty, never null */
      values: {
        [key: string]: string;
      };
    };
    PluginSettingSchema: {
      /**
       * @description Help text; empty when the plugin gives none
       * @example Two-letter country code
       */
      description: string;
      /**
       * @description JSON Schema for the value, as the plugin wrote it; empty when unconstrained
       * @example {"type":"string"}
       */
      json_schema: string;
      /**
       * @description The value's key in the installation's settings map
       * @example region
       */
      key: string;
      /**
       * @description Whether the plugin needs a value
       * @example false
       */
      required: boolean;
      /**
       * @description Display title; empty when the plugin gives none
       * @example Region
       */
      title: string;
    };
    PluginSettingsInstallation: {
      /** @description Packaged assets; empty, never null */
      assets: components["schemas"]["PluginAsset"][];
      /**
       * @description Slash-delimited grouping for the Apps navigation; absent when the manifest declares none
       * @example Tools/Utilities
       */
      category?: string;
      /**
       * @description Installation identifier
       * @example 3
       */
      id: string;
      /**
       * @description The plugin's stable identifier
       * @example org.example.subtitles
       */
      plugin_id: string;
      /** @description Routes the plugin exposes; empty, never null */
      routes: components["schemas"]["PluginRoute"][];
      /** @description Settings the plugin asks each account for; empty when it only exposes navigable routes */
      user_config_schema: components["schemas"]["PluginSettingSchema"][];
      /**
       * @description Installed version
       * @example 1.2.0
       */
      version: string;
    };
    PluginSettingsInstallationCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["PluginSettingsInstallation"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    PluginSettingsWrite: {
      /** @description The account's values, replacing the stored set; keys are the plugin's user_config_schema keys */
      values: {
        [key: string]: string;
      };
    };
    Problem: {
      /**
       * @description Safe occurrence-specific explanation; not for control flow
       * @example The request did not pass validation; see errors.
       */
      detail: string;
      /** @description Field-level validation details */
      errors?: components["schemas"]["ProblemError"][];
      /**
       * Format: uri
       * @description urn:silo:request:<request-id>, matching the X-Request-ID response header
       * @example urn:silo:request:000000000000000000000003
       */
      instance: string;
      /**
       * Format: int64
       * @description HTTP status code, equal to the response status
       * @example 422
       */
      status: number;
      /**
       * @description Short summary fixed for the problem type
       * @example Validation failed
       */
      title: string;
      /**
       * Format: uri
       * @description Stable problem type URI; the final segment is the problem identifier
       * @example https://siloserver.org/docs/api/v2/problems/validation_failed
       */
      type: string;
    };
    ProblemError: {
      /**
       * @description Stable machine-readable code for the failure
       * @example required
       */
      code: string;
      /**
       * @description Safe human-readable explanation; never the rejected value
       * @example expected required property name to be present
       */
      detail: string;
      /**
       * @description Where the error occurred: body.*, query.*, path.* or header.*
       * @example body.name
       */
      location: string;
    };
    Profile: {
      /**
       * @description Libraries the profile may see when restrictions are enabled
       * @example [
       *       "1",
       *       "2"
       *     ]
       */
      allowed_library_ids: string[];
      /** @example false */
      auto_play_next_preview: boolean;
      /** @example false */
      auto_skip_credits: boolean;
      /** @example true */
      auto_skip_intro: boolean;
      /** @example false */
      auto_skip_recap: boolean;
      /**
       * @description Avatar reference; empty when none
       * @example preset:fox
       */
      avatar: string;
      /**
       * @example preset
       * @enum {string}
       */
      avatar_source: "none" | "preset" | "upload";
      /**
       * @description Where to fetch the avatar; absent when there is none to fetch
       * @example /avatars/presets/fox.png
       */
      avatar_url?: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.000Z
       */
      created_at: string;
      /** @example false */
      has_pin: boolean;
      /**
       * @description Opaque identifier
       * @example 1
       */
      id: string;
      /** @example false */
      is_child: boolean;
      /**
       * @description The household parent (not the server admin role)
       * @example true
       */
      is_primary: boolean;
      /**
       * @description Preferred audio language (ISO 639-1); empty inherits
       * @example en
       */
      language: string;
      /** @example false */
      library_restrictions_enabled: boolean;
      /**
       * @description Content-rating ceiling; empty means none
       * @example PG-13
       */
      max_content_rating: string;
      /**
       * @description Playback ceiling. Canonical values: 1080p, 2160p; empty means none. Older profiles may carry other stored values
       * @example 1080p
       */
      max_playback_quality: string;
      /** @example Alice */
      name: string;
      /**
       * @description Metadata language (ISO 639-1); empty inherits the library's
       * @example en
       */
      preferred_metadata_language: string;
      /**
       * @description Free-form until the vocabulary is ratified (#135). Canonical values today: auto, original, 720p, 1080p, 2160p, 4k; empty when unset
       * @example auto
       */
      quality_preference: string;
      /** @example false */
      show_forced_subtitles: boolean;
      /**
       * @description Preferred subtitle language (ISO 639-1); empty inherits
       * @example en
       */
      subtitle_language: string;
      /**
       * @description Free-form until the vocabulary is ratified (#135). Canonical values today: auto, always, off, default, forced_only; empty when unset
       * @example auto
       */
      subtitle_mode: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.000Z
       */
      updated_at: string;
    };
    ProfileAvatarForm: {
      /**
       * Format: binary
       * @description The image; resized server-side to the avatar variants
       */
      avatar: string;
    };
    ProfileCollection: {
      /**
       * @description Whether this server accepts avatar uploads (an avatar store is configured)
       * @example true
       */
      avatar_upload_enabled: boolean;
      /** @description The page's items; empty, never null */
      items: components["schemas"]["Profile"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    ProfileCreate: {
      /**
       * @description Unique library identifiers the profile may see when restrictions are enabled
       * @example [
       *       "1",
       *       "2"
       *     ]
       */
      allowed_library_ids?: string[];
      /** @example false */
      auto_play_next_preview?: boolean;
      /** @example false */
      auto_skip_credits?: boolean;
      /** @example true */
      auto_skip_intro?: boolean;
      /** @example false */
      auto_skip_recap?: boolean;
      /**
       * @description Preset avatar reference
       * @example preset:fox
       */
      avatar?: string;
      /** @example false */
      is_child?: boolean;
      /**
       * @description Preferred audio language (ISO 639-1)
       * @example en
       */
      language?: string;
      /** @example false */
      library_restrictions_enabled?: boolean;
      /**
       * @description Content-rating ceiling
       * @example PG-13
       */
      max_content_rating?: string;
      /**
       * @description Playback ceiling
       * @example 1080p
       * @enum {string}
       */
      max_playback_quality?: "1080p" | "2160p";
      /**
       * @description Display name; leading and trailing spaces are trimmed
       * @example Alice
       */
      name: string;
      /**
       * @description PIN, 1 to 72 bytes
       * @example 1234
       */
      pin?: string;
      /**
       * @description Metadata language (ISO 639-1)
       * @example en
       */
      preferred_metadata_language?: string;
      /**
       * @description Free-form until the vocabulary is ratified (#135); v1 never validated it. Canonical values today: auto, original, 720p, 1080p, 2160p, 4k
       * @example auto
       */
      quality_preference?: string;
      /**
       * @description Defaults to true when omitted
       * @example true
       */
      show_forced_subtitles?: boolean;
      /**
       * @description Preferred subtitle language (ISO 639-1)
       * @example en
       */
      subtitle_language?: string;
      /**
       * @description Free-form until the vocabulary is ratified (#135); v1 never validated it. Canonical values today: auto, always, off, default, forced_only
       * @example auto
       */
      subtitle_mode?: string;
    };
    ProfilePINCheck: {
      /**
       * @description The PIN to check
       * @example 1234
       */
      pin: string;
    };
    ProfileSectionFlags: {
      /**
       * @description Whether non-admin profiles may build sections from admin-only recipes
       * @example false
       */
      allow_profile_custom_sections: boolean;
    };
    ProfileSectionSetting: {
      /** @description The recipe's effective config; absent when none, {} when explicitly empty */
      config?: {
        [key: string]: unknown;
      };
      /**
       * @description An admin section the profile has changed
       * @example true
       */
      customized: boolean;
      /** @example false */
      featured: boolean;
      /**
       * @description Hidden by the profile
       * @example false
       */
      hidden: boolean;
      /** @example s-continue-watching */
      id: string;
      /**
       * @description Built by the profile rather than defined by an administrator
       * @example false
       */
      is_custom: boolean;
      /**
       * Format: int64
       * @example 20
       */
      item_limit: number;
      /**
       * Format: int64
       * @example 0
       */
      position: number;
      /**
       * @description The recipe; the set is extensible
       * @example continue_watching
       */
      section_type: string;
      /** @example Continue Watching */
      title: string;
    };
    ProfileSectionSettingCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["ProfileSectionSetting"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    ProfileUpdate: {
      /**
       * @description Replaces the allowlist with these unique library identifiers; an empty array allows none
       * @example [
       *       "1",
       *       "2"
       *     ]
       */
      allowed_library_ids?: string[];
      /** @example false */
      auto_play_next_preview?: boolean;
      /** @example false */
      auto_skip_credits?: boolean;
      /** @example true */
      auto_skip_intro?: boolean;
      /** @example false */
      auto_skip_recap?: boolean;
      /**
       * @description Preset avatar reference; null removes the avatar
       * @example preset:fox
       */
      avatar?: string | null;
      /** @example false */
      is_child?: boolean;
      /**
       * @description Preferred audio language (ISO 639-1); null inherits
       * @example en
       */
      language?: string | null;
      /** @example false */
      library_restrictions_enabled?: boolean;
      /**
       * @description Content-rating ceiling; null removes it
       * @example PG-13
       */
      max_content_rating?: string | null;
      /**
       * @description Playback ceiling; null removes it
       * @example 1080p
       * @enum {string|null}
       */
      max_playback_quality?: "1080p" | "2160p" | null;
      /**
       * @description Display name; leading and trailing spaces are trimmed
       * @example Alice
       */
      name?: string;
      /**
       * @description New PIN, 1 to 72 bytes; null removes the PIN. An empty string is rejected, not a clear
       * @example 1234
       */
      pin?: string | null;
      /**
       * @description Metadata language (ISO 639-1); null inherits the library's
       * @example en
       */
      preferred_metadata_language?: string | null;
      /**
       * @description Free-form until the vocabulary is ratified (#135); v1 never validated it. Canonical values today: auto, original, 720p, 1080p, 2160p, 4k
       * @example auto
       */
      quality_preference?: string;
      /** @example false */
      show_forced_subtitles?: boolean;
      /**
       * @description Preferred subtitle language (ISO 639-1); null inherits
       * @example en
       */
      subtitle_language?: string | null;
      /**
       * @description Free-form until the vocabulary is ratified (#135); v1 never validated it. Canonical values today: auto, always, off, default, forced_only
       * @example auto
       */
      subtitle_mode?: string;
    };
    ProfileVerification: {
      /**
       * Format: date-time
       * @description When the token stops being accepted; null when no token was issued or it does not expire
       * @example 2026-01-02T15:04:05.000Z
       */
      expires_at: string | null;
      /**
       * @description Send as X-Profile-Token with X-Profile-Id to act as the unlocked profile; bound to this login session. Absent when the PIN did not match
       * @example pvt_5f3a9c1e7b2d4e8fa0c6
       */
      profile_token?: string;
      /**
       * @description Whether the PIN matched
       * @example true
       */
      valid: boolean;
    };
    ProgressCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["ProgressEntry"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    ProgressEntry: {
      /**
       * @description Whether the item counts as watched
       * @example false
       */
      completed: boolean;
      /**
       * Format: double
       * @description Known runtime; 0 when unknown
       * @example 5400
       */
      duration_seconds: number;
      /**
       * @description The catalog item
       * @example movie-8f2c1a
       */
      media_item_id: string;
      /**
       * Format: double
       * @description Playback position
       * @example 1325.5
       */
      position_seconds: number;
      /**
       * Format: date-time
       * @description When the position last changed
       * @example 2026-01-02T03:04:05.000Z
       */
      updated_at: string;
    };
    ProgressSyncInputBody: {
      /** @description Writes to apply, in order */
      items: components["schemas"]["ProgressSyncItem"][];
    };
    ProgressSyncItem: {
      /**
       * Format: int64
       * @description Known runtime in milliseconds; 0 when unknown
       * @example 5400000
       */
      duration_ms: number;
      /**
       * @description Write the position as given instead of merging it with the stored one
       * @example false
       */
      force_overwrite?: boolean;
      /**
       * @description The catalog item
       * @example movie-8f2c1a
       */
      media_item_id: string;
      /**
       * Format: int64
       * @description Playback position in milliseconds
       * @example 1325500
       */
      position_ms: number;
      /**
       * Format: date-time
       * @description Client event time of an offline-queued write; the server clamps it to now and merges last-write-wins on it
       * @example 2026-01-02T03:04:05.000Z
       */
      updated_at?: string;
    };
    ProgressSyncOutputBody: {
      /** @description One per item, in request order */
      results: components["schemas"]["ProgressSyncResult"][];
    };
    ProgressSyncResult: {
      /**
       * @description Why the write was not applied
       * @example failed to update progress
       */
      error?: string;
      /**
       * @description Opaque identifier
       * @example movie-8f2c1a
       */
      media_item_id: string;
      /**
       * @description ok when the write was applied (or skipped as below a threshold); error when it was not
       * @example ok
       * @enum {string}
       */
      status: "ok" | "error";
    };
    ProviderChainEntry: {
      /** @example tmdb */
      capability_id: string;
      /** @example true */
      enabled: boolean;
      /**
       * @description Opaque identifier
       * @example 3
       */
      plugin_installation_id: string;
      /**
       * Format: int64
       * @description Lower runs first
       * @example 0
       */
      priority: number;
      /** @example tmdb */
      provider_slug: string;
    };
    ProviderChainEntryInput: {
      /** @example tmdb */
      capability_id: string;
      /** @example true */
      enabled: boolean;
      /**
       * @description Opaque identifier
       * @example 3
       */
      plugin_installation_id: string;
      /**
       * Format: int64
       * @description Lower runs first
       * @example 0
       */
      priority: number;
    };
    ProviderChainLevel: {
      /** @example movie */
      content_level: string;
      entries: components["schemas"]["ProviderChainEntry"][];
    };
    ProviderChainLevelInput: {
      /** @example movie */
      content_level: string;
      entries: components["schemas"]["ProviderChainEntryInput"][];
    };
    RatingCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["RatingEntry"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    RatingEntry: {
      /**
       * @description The catalog item
       * @example movie:heat-1995
       */
      item_id: string;
      /**
       * Format: date-time
       * @description When the rating was last set
       * @example 2026-01-02T03:04:05.000Z
       */
      rated_at: string;
      /**
       * Format: int64
       * @description Stars, 1 to 5
       * @example 4
       */
      rating: number;
    };
    RatingSet: {
      /**
       * Format: int64
       * @description Stars, 1 to 5
       * @example 4
       */
      rating: number;
    };
    RawMatchBacklogEntry: {
      /** @example unknown */
      base_title?: string;
      /** @example movie */
      base_type?: string;
      /** Format: int64 */
      base_year?: number;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      created_at: string;
      /** @example /media/movies/unknown.mkv */
      file_path: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       */
      last_attempted_at?: string;
      /**
       * @description Opaque identifier
       * @example 1
       */
      library_id: string;
      /**
       * @description Opaque identifier
       * @example 121
       */
      media_file_id: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      updated_at: string;
    };
    RecipeCandidate: {
      /** @example Action */
      display_name: string;
      /** @description Empty when the candidate has none */
      subtitle: string;
      /** @example action */
      value: string;
    };
    RecipeCandidateCollection: {
      /** @description Empty, never null */
      candidates: components["schemas"]["RecipeCandidate"][];
    };
    RecipeCatalog: {
      /** @description Empty, never null */
      categories: components["schemas"]["RecipeCategory"][];
    };
    RecipeCategory: {
      /** @example library_staples */
      category: string;
      /** @description Empty, never null */
      recipes: components["schemas"]["RecipeDefinition"][];
    };
    RecipeDefinition: {
      /** @example false */
      admin_only: boolean;
      /** @example true */
      avoid_duplicates: boolean;
      /** @example library_staples */
      category: string;
      /** @description Empty, never null */
      presets: components["schemas"]["RecipePreset"][];
      /** @example false */
      supports_rotation: boolean;
      /** @example recently_added */
      type: string;
    };
    RecipePreset: {
      /** @description The config the preset starts from; empty, never null */
      default_params: {
        [key: string]: unknown;
      };
      /** @description Empty when the preset has none */
      description_long: string;
      /** @example Items added in the last seven days */
      description_short: string;
      /** @example New this week */
      display_name: string;
      /** @example sparkles */
      icon: string;
      /** @example new_this_week */
      key: string;
    };
    RecommendationRow: {
      /** @description Empty, never null */
      items: components["schemas"]["CatalogItem"][];
      /**
       * @description Section key paired with kind (cluster index or genre name)
       * @example 0
       */
      key?: string;
      /**
       * @description Section kind for getRecommendationSection; absent when the row has no dedicated page
       * @example cluster
       */
      kind?: string;
      /**
       * @description Display label
       * @example Because you enjoy Crime
       */
      title: string;
      /**
       * @description Row type: cluster, similar_users_liked, popular, recently_added, top_rated, genre_sampler
       * @example cluster
       */
      type: string;
    };
    RecommendationRowCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["RecommendationRow"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    RootOverride: {
      /** @example tt0113277 */
      forced_imdb_id?: string;
      /** @example Heat */
      forced_title?: string;
      /** @example 949 */
      forced_tmdb_id?: string;
      forced_tvdb_id?: string;
      /** @example movie */
      forced_type?: string;
      /**
       * Format: int64
       * @example 1995
       */
      forced_year?: number;
      note?: string;
    };
    RootOverrideSet: {
      /** @example tt0113277 */
      forced_imdb_id?: string;
      /** @example Heat */
      forced_title?: string;
      /** @example 949 */
      forced_tmdb_id?: string;
      forced_tvdb_id?: string;
      /** @example movie */
      forced_type?: string;
      /**
       * Format: int64
       * @example 1995
       */
      forced_year?: number;
      /**
       * @description Opaque identifier
       * @example 1
       */
      library_id: string;
      note?: string;
      /**
       * @description The root, as listLibraryRoots reports it
       * @example /media/movies/Heat (1995)
       */
      root_path: string;
    };
    Section: {
      /** @example false */
      customized: boolean;
      /** @example false */
      featured: boolean;
      /** @example recently_added */
      id: string;
      /** @example false */
      is_custom: boolean;
      /**
       * Format: int64
       * @example 20
       */
      item_limit: number;
      /** @description Empty, never null */
      items: components["schemas"]["CatalogItem"][];
      /** @example recently_added */
      section_type: string;
      /** @example Recently Added */
      title: string;
      /**
       * Format: int64
       * @description Items the section has in total, at least the number returned
       * @example 120
       */
      total_count: number;
    };
    SectionCollection: {
      /** @description Empty, never null */
      sections: components["schemas"]["Section"][];
    };
    SectionLayout: {
      /** @description Empty, never null */
      sections: components["schemas"]["SectionLayoutEntry"][];
    };
    SectionLayoutEntry: {
      /**
       * @description The profile has overridden the section's placement or visibility
       * @example false
       */
      customized: boolean;
      /** @example false */
      featured: boolean;
      /** @example recently_added */
      id: string;
      /**
       * @description An administrator-defined section rather than a built-in one
       * @example false
       */
      is_custom: boolean;
      /**
       * Format: int64
       * @example 20
       */
      item_limit: number;
      /** @example recently_added */
      section_type: string;
      /** @example Recently Added */
      title: string;
    };
    SectionOverride: {
      /** @description Legacy config override; absent when the profile saved none (the section's own config applies), {} when it saved an empty one */
      config?: {
        [key: string]: unknown;
      };
      /**
       * Format: date-time
       * @description When the row was saved; null when the store did not record it
       * @example 2026-01-02T03:04:05.000Z
       */
      created_at: string | null;
      /**
       * @description Featured override; null keeps the admin value
       * @example false
       */
      featured: boolean | null;
      /** @example false */
      hidden: boolean;
      /**
       * @description The override's identifier; empty when the client saved none
       * @example c6b0f2a8-1a3e-4d55-9a6f-0b7e6c1d2f30
       */
      id: string;
      /**
       * @description A profile-built section rather than a customization of an admin one
       * @example false
       */
      is_user_added: boolean;
      /**
       * Format: int64
       * @description Item-limit override; null keeps the admin value
       * @example 20
       */
      item_limit: number | null;
      /**
       * Format: int64
       * @description Position override; null keeps the admin order
       * @example 2
       */
      position: number | null;
      /**
       * @description Removed from the page by the profile
       * @example false
       */
      removed: boolean;
      /**
       * @description The admin section this customizes; empty for a profile-built section
       * @example s-continue-watching
       */
      section_id: string;
      /**
       * @description Legacy recipe name; empty when unset
       * @example recently_added
       */
      section_type: string;
      /**
       * @description Title override; empty keeps the admin title
       * @example New this week
       */
      title: string;
      /**
       * Format: date-time
       * @description When the row was last saved; null when the store did not record it
       * @example 2026-01-02T03:04:05.000Z
       */
      updated_at: string | null;
      /** @description Config of a profile-built section; absent when the profile saved none (config applies), {} when it saved an empty one */
      user_config?: {
        [key: string]: unknown;
      };
      /**
       * @description Recipe of a profile-built section; empty otherwise
       * @example hidden_gems
       */
      user_section_type: string;
      /**
       * @description Title of a profile-built section; empty otherwise
       * @example Hidden gems
       */
      user_title: string;
    };
    SectionOverrideCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["SectionOverride"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    SectionOverrideSet: {
      /** @description The page's complete override set; replaces what was saved */
      overrides: components["schemas"]["SectionOverrideWrite"][];
    };
    SectionOverrideWrite: {
      /** @description Legacy config override; {} saves an explicitly empty one, omitted saves none. Validated by the recipe on a profile-built section */
      config?: {
        [key: string]: unknown;
      };
      /**
       * @description Featured override; null or omitted keeps the admin value
       * @example false
       */
      featured?: boolean | null;
      /** @example false */
      hidden?: boolean;
      /**
       * @description Client-chosen identifier; a UUID by convention
       * @example c6b0f2a8-1a3e-4d55-9a6f-0b7e6c1d2f30
       */
      id?: string;
      /**
       * @description A profile-built section; an empty section_id implies it
       * @example false
       */
      is_user_added?: boolean;
      /**
       * Format: int64
       * @description Item-limit override; null or omitted keeps the admin value
       * @example 20
       */
      item_limit?: number | null;
      /**
       * Format: int64
       * @description Position override; null or omitted keeps the admin order
       * @example 2
       */
      position?: number | null;
      /** @example false */
      removed?: boolean;
      /**
       * @description The admin section to customize; omitted or empty for a profile-built section
       * @example s-continue-watching
       */
      section_id?: string;
      /**
       * @description Legacy recipe name; user_section_type is preferred for a profile-built section
       * @example recently_added
       */
      section_type?: string;
      /**
       * @description Title override; empty keeps the admin title
       * @example New this week
       */
      title?: string;
      /** @description Config of a profile-built section; {} saves an explicitly empty one, omitted saves none. Validated by the recipe */
      user_config?: {
        [key: string]: unknown;
      };
      /**
       * @description Recipe of a profile-built section; must be registered on this server
       * @example hidden_gems
       */
      user_section_type?: string;
      /** @example Hidden gems */
      user_title?: string;
    };
    SeriesMatchQueueEntry: {
      /**
       * Format: int64
       * @example 1
       */
      attempt_count: number;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      available_at: string;
      /**
       * Format: int64
       * @example 0
       */
      deterministic_attempt_count: number;
      /** @description Matcher-specific failure document */
      failure_detail?: unknown;
      failure_kind?: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      first_queued_at: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       */
      last_attempted_at?: string;
      last_error?: string;
      /**
       * @description Opaque identifier
       * @example 1
       */
      library_id: string;
      /**
       * Format: int64
       * @example 3
       */
      matcher_revision: number;
      /** @example /media/tv/Severance */
      observed_root_path: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       */
      parked_at?: string;
      /** @example pending */
      state: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      updated_at: string;
    };
    SettingConstraint: {
      /**
       * @description How the policy narrows the value; one of the settings contract's constraint kinds
       * @example ceiling
       */
      constraint: string;
      /**
       * @description The access-policy input the constraint reads
       * @example max_playback_quality
       */
      policy_input: string;
    };
    SettingContextRequest: {
      /**
       * @description Caller-chosen identifier echoed on the matching result; unique within the request
       * @example row-1
       */
      context_id: string;
      /**
       * @description The library the context is in; this or series_id is required
       * @example 3
       */
      library_id?: string;
      /**
       * @description The series the context is in; this or library_id is required
       * @example tv:12345
       */
      series_id?: string;
    };
    SettingsContractCapabilities: {
      /**
       * Format: int64
       * @description Settings protocol version; changes only for a change no revision rule can express
       * @example 1
       */
      api_version: number;
      /**
       * @description Client families a profile_client scope may name
       * @example [
       *       "tv",
       *       "mobile"
       *     ]
       */
      client_families: string[];
      /**
       * @description Entity tag of the public manifest getSettingsContract serves
       * @example "a1b2c3"
       */
      contract_etag: string;
      /**
       * Format: int64
       * @description Number of setting definitions in the manifest
       * @example 40
       */
      definition_count: number;
      /**
       * Format: int64
       * @description Manifest revision clients filter definitions against
       * @example 12
       */
      revision: number;
      /**
       * @description Setting scopes this server resolves
       * @example [
       *       "account",
       *       "profile"
       *     ]
       */
      scopes: string[];
      /**
       * @description Whether navigation shortcut list mutations are atomic
       * @example true
       */
      supports_atomic_shortcuts: boolean;
      /**
       * @description Whether the effective resolver accepts a batch of keys
       * @example true
       */
      supports_batched_effective: boolean;
      /**
       * @description Whether repeating a value write converges on the same state
       * @example true
       */
      supports_idempotent_writes: boolean;
    };
    SettingSourceContext: {
      /**
       * @description The client family of the winning row
       * @example tv
       */
      client_family?: string;
      /**
       * @description The device of the winning row
       * @example iphone-1
       */
      device_id?: string;
      /**
       * @description The library of the winning row
       * @example 3
       */
      library_id?: string;
      /**
       * @description The profile of the winning row
       * @example 1
       */
      profile_id?: string;
      /**
       * @description The series of the winning row
       * @example tv:12345
       */
      series_id?: string;
    };
    SettingValue: {
      /**
       * @description The client family of a profile_client value
       * @example tv
       */
      client_family?: string;
      /**
       * @description The device of a profile_device value
       * @example iphone-1
       */
      device_id?: string;
      /**
       * @description The setting key
       * @example ui.theme
       */
      key: string;
      /**
       * @description The library of a profile_library value
       * @example 3
       */
      library_id?: string;
      /**
       * @description The profile the value belongs to; absent at account scope
       * @example 1
       */
      profile_id?: string;
      /**
       * Format: int64
       * @description The row's revision, incremented on every write
       * @example 4
       */
      revision: number;
      /**
       * @description The scope the value is stored at: account, profile, profile_client, profile_device, profile_library or profile_series
       * @example profile
       */
      scope: string;
      /**
       * @description The series of a profile_series value
       * @example tv:12345
       */
      series_id?: string;
      /**
       * Format: date-time
       * @description When the value was last written; absent when the store did not record it
       * @example 2026-01-02T03:04:05.000Z
       */
      updated_at?: string;
      /** @description The stored value, normalized to the key's value_schema */
      value: unknown;
    };
    SettingValueCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["ExplicitSettingValue"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
      /**
       * Format: int64
       * @description The settings contract revision this server serves
       * @example 8
       */
      revision: number;
    };
    SettingValueWrite: {
      /** @description The value to store; validated and normalized against the key's value_schema */
      value: unknown;
    };
    SetupStatus: {
      /**
       * @description True until the first administrator account exists
       * @example false
       */
      needs_setup: boolean;
    };
    SkippedRoot: {
      /**
       * Format: int64
       * @example 3
       */
      file_count: number;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      first_seen_at: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      last_seen_at: string;
      /**
       * @description Opaque identifier
       * @example 1
       */
      library_id: string;
      /** @example Movies */
      library_name: string;
      /** @example no_media_files */
      reason: string;
      /** @example /media/movies/Extras */
      root_path: string;
      /** @example /media/movies/Extras/notes.txt */
      sample_file_path: string;
    };
    SkippedRootCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["SkippedRoot"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    StaleMediaID: {
      /** @example movie:heat-1995 */
      content_id: string;
      /** @example movie */
      content_type: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      first_seen_at: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      last_seen_at: string;
      /**
       * @description "0" when the item has no file in any library
       * @example 1
       */
      library_id: string;
      /** @example Movies */
      library_name: string;
      /** @example tmdb */
      provider: string;
      /** @example 949 */
      provider_id: string;
      /** @example Heat */
      title: string;
      /**
       * Format: int64
       * @example 1995
       */
      year: number;
    };
    StaleMediaIDCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["StaleMediaID"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    SubtitleAppearanceDeviceOverride: {
      /**
       * @description The subtitle appearance as a JSON document; the server validates only that it is JSON
       * @example {"fontSize":"xxlarge"}
       */
      value: string;
    };
    SubtitlePreference: {
      /**
       * @description Path of the chosen external subtitle file; absent for an embedded track
       * @example Show.S01E01.eng.srt
       */
      external_subtitle_path?: string;
      /**
       * @description Opaque identifier
       * @example p-1
       */
      profile_id: string;
      /**
       * @description The series content id
       * @example series-8f2c1a
       */
      series_id: string;
      /**
       * @description Forced-subtitle override; absent when the profile has none for the series
       * @example true
       */
      show_forced_subtitles?: boolean;
      /**
       * @description Preferred subtitle language; empty means no language preference
       * @example eng
       */
      subtitle_language: string;
      /**
       * @description Subtitle mode. Canonical values: auto, always, off; empty means no mode preference
       * @example always
       */
      subtitle_mode: string;
      /**
       * Format: int64
       * @description Zero-based index of the chosen track, or -1 for subtitles off
       * @example 0
       */
      subtitle_track_index: number;
      /** @description Absent when the client stored none */
      track_signature?: components["schemas"]["SubtitleTrackSignature"];
      /**
       * Format: date-time
       * @description Last update; Unix epoch when an older SQLite track preference has no recorded timestamp
       * @example 2026-01-02T03:04:05.000Z
       */
      updated_at: string;
    };
    SubtitlePreferenceUpdate: {
      /**
       * @description Path of the chosen external subtitle file, when the track is external
       * @example Show.S01E01.eng.srt
       */
      external_subtitle_path?: string;
      /**
       * @description Forced-subtitle override for the series; omitted keeps the stored override
       * @example true
       */
      show_forced_subtitles?: boolean;
      /**
       * @description Preferred subtitle language; empty or omitted means no language preference
       * @example eng
       */
      subtitle_language?: string;
      /**
       * @description Subtitle mode: auto, always or off; empty or omitted means no mode preference
       * @example always
       */
      subtitle_mode?: string;
      /**
       * Format: int64
       * @description Zero-based index of the chosen track, or -1 for subtitles off
       * @example 0
       */
      subtitle_track_index: number;
      /** @description How playback recognizes the track when indexes shift */
      track_signature?: components["schemas"]["SubtitleTrackSignature"];
    };
    SubtitleTrackSignature: {
      /** @example subrip */
      codec?: string;
      /**
       * @description Whether the track carried the forced flag
       * @example false
       */
      forced?: boolean;
      /**
       * @description Whether the track carried the hearing-impaired flag
       * @example true
       */
      hearing_impaired?: boolean;
      /**
       * @description Display label the client showed
       * @example English (SDH)
       */
      label?: string;
      /**
       * @description Track language (ISO 639)
       * @example eng
       */
      language?: string;
      /**
       * @description Where the track comes from, e.g. embedded or external
       * @example embedded
       */
      source?: string;
    };
    SystemInfo: {
      /**
       * Format: int64
       * @description The native API major this server serves at this path
       * @example 2
       */
      api_major: number;
      /**
       * @description SHA-256 (hex) of the exact committed OpenAPI artifact served at links.openapi. Diagnostic and cache-identity only: never feature-detect on it.
       * @example 3b8f0c2d9e1a4f6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b
       */
      contract_digest: string;
      /** @description Stable links to the contract and to capability documents */
      links: components["schemas"]["SystemInfoLinks"];
      /**
       * @description Server build identity (short revision, +dirty when built from a modified tree, or "unavailable"). Diagnostic only: never feature-detect on it.
       * @example 1.0.0-dev
       */
      server_version: string;
    };
    SystemInfoLinks: {
      /**
       * @description Path prefix of the per-domain capability documents
       * @example /api/v2/capabilities
       */
      capabilities: string;
      /**
       * @description Path of the committed OpenAPI artifact
       * @example /api/v2/openapi.json
       */
      openapi: string;
    };
    TasteProfile: {
      /** @description Empty, never null */
      favorite_directors: string[];
      /** @description How many signals of each kind (rated, favorited, watched, …) fed the profile; empty, never null */
      signal_counts: {
        [key: string]: number;
      };
      /** @description Strongest genres, best first; empty, never null */
      top_genres: string[];
      /**
       * Format: date-time
       * @description When the profile was last computed; absent until it has been
       */
      updated_at?: string;
    };
    TasteSeedResult: {
      /**
       * Format: int64
       * @description Picks recorded as favorites by this submission
       * @example 3
       */
      added: number;
    };
    TasteSeedSubmission: {
      /**
       * @description Picked catalog identifiers; each is recorded as a favorite
       * @example [
       *       "movie:heat-1995"
       *     ]
       */
      item_ids: string[];
    };
    UnmatchedItem: {
      /** @example movie:heat-1995 */
      content_id: string;
      /** @example movie */
      content_type: string;
      /**
       * @description "0" when the item is in no library
       * @example 1
       */
      library_id: string;
      /** @example Movies */
      library_name: string;
      /**
       * @description unmatched, pending or ambiguous
       * @example unmatched
       */
      status: string;
      /** @example Heat */
      title: string;
      /**
       * Format: int64
       * @example 1995
       */
      year: number;
    };
    UnmatchedItemCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["UnmatchedItem"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
      /**
       * Format: int64
       * @description Items matching the filter across every page
       * @example 1
       */
      total: number;
    };
    UserCollection: {
      /** @example manual */
      collection_type: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      created_at: string;
      /** @example p-owner */
      creator_profile_id: string;
      description?: string;
      /** @example 01J9Z8C3W4R5T6Y7U8I9O0P1Q4 */
      id: string;
      /**
       * Format: int64
       * @example 4
       */
      item_count: number;
      /** @example Rainy days */
      name: string;
      poster_thumbhash?: string;
      /** @description Presigned, short-lived */
      poster_url?: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.678Z
       */
      updated_at: string;
    };
    UserCollectionCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["UserCollection"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    WatchAudioTrack: {
      /** Format: int64 */
      bit_depth?: number;
      /** Format: int64 */
      bitrate?: number;
      /** Format: int64 */
      channels?: number;
      /** @example eac3 */
      codec?: string;
      default: boolean;
      embedded_title?: string;
      /** @example eng */
      language?: string;
      layout?: string;
      profile?: string;
      /** Format: int64 */
      sample_rate?: number;
      title?: string;
    };
    WatchChapter: {
      /** Format: double */
      end_seconds: number;
      /** Format: int64 */
      index: number;
      /** @description Where the chapter came from */
      source: string;
      /** Format: double */
      start_seconds: number;
      thumbnail_thumbhash?: string;
      thumbnail_url?: string;
      title: string;
    };
    WatchDetail: {
      /** @example movie:heat-1995 */
      content_id: string;
      credits?: components["schemas"]["WatchMarker"];
      effective_show_forced_subtitles?: boolean;
      /** @description The subtitle language the profile's preferences resolve to */
      effective_subtitle_language?: string;
      effective_subtitle_mode?: string;
      /** @description The subtitle track the profile last chose on this item */
      effective_subtitle_track_signature?: components["schemas"]["WatchSubtitleSignature"];
      effective_version_codec_video?: string;
      effective_version_edition_key?: string;
      effective_version_hdr?: boolean;
      /** @description The version resolution the profile last played */
      effective_version_resolution?: string;
      /** Format: int64 */
      episode_number?: number;
      intro?: components["schemas"]["WatchMarker"];
      overview?: string;
      /** @description Logical watch choices, each spanning one or more ordered parts */
      playback_variants?: components["schemas"]["WatchPlaybackVariant"][];
      preview?: components["schemas"]["WatchMarker"];
      recap?: components["schemas"]["WatchMarker"];
      /** Format: int64 */
      season_number?: number;
      /** @description Owning series of an episode */
      series_id?: string;
      series_title?: string;
      /** @description Empty, never null */
      subtitles: components["schemas"]["WatchSubtitle"][];
      /** @example Heat */
      title: string;
      /**
       * @description movie, episode, audiobook, ebook
       * @example movie
       */
      type: string;
      /** @description The acting profile's progress; absent without a profile or progress */
      user_data?: components["schemas"]["WatchUserData"];
      /** @description Every playable file of the item; empty, never null */
      versions: components["schemas"]["WatchFileVersion"][];
      /**
       * Format: int64
       * @example 1995
       */
      year?: number;
    };
    WatchFileVersion: {
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       * @example 2026-01-02T03:04:05.000Z
       */
      added_at: string;
      audio_tracks?: components["schemas"]["WatchAudioTrack"][];
      /**
       * Format: int64
       * @description Bits per second
       */
      bitrate: number;
      chapters?: components["schemas"]["WatchChapter"][];
      /** @example eac3 */
      codec_audio: string;
      /** @example h264 */
      codec_video: string;
      /** @example mkv */
      container: string;
      credits?: components["schemas"]["WatchMarker"];
      /**
       * Format: int64
       * @example 10200
       */
      duration_seconds: number;
      edition_key?: string;
      edition_raw?: string;
      effective_audio_language?: string;
      /** Format: int64 */
      effective_audio_track_index?: number;
      /**
       * @description Opaque identifier
       * @example 42
       */
      file_id: string;
      file_name?: string;
      /** @description Only for accounts allowed to see paths */
      file_path?: string;
      /**
       * Format: int64
       * @description Bytes
       */
      file_size: number;
      hdr: boolean;
      intro?: components["schemas"]["WatchMarker"];
      /** Format: int64 */
      multi_episode_end?: number;
      /** Format: int64 */
      multi_episode_start?: number;
      presentation_group_key?: string;
      presentation_kind?: string;
      /** Format: int64 */
      presentation_part_index?: number;
      /** Format: int64 */
      presentation_part_total?: number;
      preview?: components["schemas"]["WatchMarker"];
      recap?: components["schemas"]["WatchMarker"];
      /** @example 1080p */
      resolution: string;
      subtitle_tracks?: components["schemas"]["WatchSubtitleTrack"][];
      video_tracks?: components["schemas"]["WatchVideoTrack"][];
    };
    WatchlistCollection: {
      /** @description The page's items; empty, never null */
      items: components["schemas"]["CatalogItem"][];
      /** @description Cursor state; absent for bounded unpaginated collections */
      page?: components["schemas"]["PageInfo"];
    };
    WatchlistEntry: {
      /**
       * Format: date-time
       * @description When the item joined the watchlist
       * @example 2026-01-02T03:04:05.000Z
       */
      added_at: string;
      /**
       * @description The catalog item
       * @example movie:heat-1995
       */
      item_id: string;
    };
    WatchMarker: {
      /**
       * Format: double
       * @example 90
       */
      end_seconds: number;
      /**
       * Format: double
       * @example 0
       */
      start_seconds: number;
    };
    WatchPlaybackVariant: {
      /**
       * @description Opaque identifier
       * @example 1
       */
      default_file_id?: string;
      edition_key?: string;
      edition_raw?: string;
      /** Format: int64 */
      part_count: number;
      /** @description Ordered; empty, never null */
      parts: components["schemas"]["WatchPlaybackVariantPart"][];
      presentation_group_key?: string;
      presentation_kind?: string;
      /** Format: int64 */
      total_duration_seconds?: number;
      variant_id: string;
    };
    WatchPlaybackVariantPart: {
      /**
       * @description Opaque identifier
       * @example 1
       */
      default_file_id?: string;
      /** Format: int64 */
      part_index: number;
      /** Format: int64 */
      total_duration_seconds?: number;
      /** @description Empty, never null */
      versions: components["schemas"]["WatchFileVersion"][];
    };
    WatchSubtitle: {
      codec?: string;
      forced: boolean;
      hearing_impaired: boolean;
      /** @example eng */
      language: string;
      /**
       * @description embedded or external
       * @example embedded
       */
      source: string;
      title?: string;
    };
    WatchSubtitleSignature: {
      codec?: string;
      forced: boolean;
      hearing_impaired: boolean;
      label?: string;
      language?: string;
      source?: string;
    };
    WatchSubtitleTrack: {
      codec?: string;
      default: boolean;
      embedded_title?: string;
      external: boolean;
      file_name?: string;
      forced: boolean;
      hearing_impaired: boolean;
      /** Format: int64 */
      index?: number;
      /** @example eng */
      language?: string;
      resolution?: string;
      title?: string;
    };
    WatchTonight: {
      /**
       * @description True when the profile has no taste profile and nothing in progress, so the list is a generic fallback
       * @example false
       */
      is_cold: boolean;
      /** @description Best first; empty, never null */
      items: components["schemas"]["WatchTonightItem"][];
    };
    WatchTonightCard: {
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       */
      added_at?: string;
      backdrop_thumbhash?: string;
      /** @description Presigned, short-lived */
      backdrop_url?: string;
      /** @description Section-specific badges (new, returning, …) */
      badges?: string[];
      /** @description Up to four cast members; empty, never null */
      cast: components["schemas"]["WatchTonightCastMember"][];
      /**
       * @description Deterministic catalog identifier
       * @example movie:heat-1995
       */
      content_id: string;
      /** @example R */
      content_rating?: string;
      /** Format: double */
      duration_seconds?: number;
      /** Format: int64 */
      episode_number?: number;
      /** @description Empty, never null */
      genres: string[];
      /** @description On a continue-watching card: in_progress or next_up */
      item_source?: string;
      /** @description Empty, never null */
      keywords: string[];
      /** @description Calendar date, YYYY-MM-DD */
      last_air_date?: string;
      /** @description Presigned, short-lived */
      logo_url?: string;
      /** Format: int64 */
      manga_chapter_count?: number;
      /** Format: int64 */
      manga_volume_count?: number;
      networks?: string[];
      /** @example en */
      original_language?: string;
      /** @description Technical badges of the best file */
      overlay_summary?: components["schemas"]["CatalogItemOverlay"];
      overview?: string;
      /** @description The item to play when the card is a series or season; absent when the item plays itself */
      play_content_id?: string;
      /**
       * Format: double
       * @description Resume position on a continue-watching card
       */
      position_seconds?: number;
      poster_thumbhash?: string;
      /** @description Presigned, short-lived */
      poster_url?: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       */
      progress_updated_at?: string;
      /** Format: double */
      rating_imdb?: number;
      /** Format: int64 */
      rating_rt_audience?: number;
      /** Format: int64 */
      rating_rt_critic?: number;
      /** Format: double */
      rating_tmdb?: number;
      /**
       * @description Calendar date, YYYY-MM-DD
       * @example 1995-12-15
       */
      release_date?: string;
      /**
       * Format: int64
       * @description Minutes
       * @example 170
       */
      runtime?: number;
      /** Format: int64 */
      season_number?: number;
      /** @description Owning series of an episode or season */
      series_id?: string;
      series_title?: string;
      /** @description Airing state of a series */
      show_status?: string;
      /** @description The values the listing was sorted by */
      sort_metrics?: components["schemas"]["CatalogItemSortMetrics"];
      /**
       * @description Metadata match state of the item
       * @example matched
       */
      status: string;
      studios?: string[];
      /** @example Heat */
      title: string;
      /**
       * @description movie, series, season, episode, audiobook, ebook, podcast, podcast_episode
       * @example movie
       */
      type: string;
      upcoming_event?: components["schemas"]["CatalogItemUpcomingEvent"];
      /** @description The viewer's flags; absent without a profile */
      user_state?: components["schemas"]["CatalogItemUserState"];
      /**
       * @description Which pool the card came from
       * @example recommendation
       * @enum {string}
       */
      watch_tonight_source: "continue_watching" | "next_up" | "recommendation";
      /** @description Sibling editions of the same work */
      work_formats?: components["schemas"]["CatalogWorkFormat"][];
      /** @description The work (book) an audiobook or ebook edition belongs to */
      work_id?: string;
      work_title?: string;
      /**
       * Format: int64
       * @example 1995
       */
      year?: number;
    };
    WatchTonightCardPage: {
      /**
       * @description Whether eligible cards remain beyond this page; paging_limited may require restarting the swipe session
       * @example true
       */
      has_more: boolean;
      /**
       * @description True when the cards are a generic fallback rather than taste-driven
       * @example false
       */
      is_cold: boolean;
      /** @description Empty, never null */
      items: components["schemas"]["WatchTonightCard"][];
      /**
       * @description True when eligible cards remain but the 200-card exclusion budget is full; restart with no exclusions
       * @example false
       */
      paging_limited: boolean;
    };
    WatchTonightCastMember: {
      /** @example Lt. Vincent Hanna */
      character?: string;
      /** @example Al Pacino */
      name: string;
      /** @description Presigned, short-lived */
      photo_url?: string;
    };
    WatchTonightItem: {
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       */
      added_at?: string;
      backdrop_thumbhash?: string;
      /** @description Presigned, short-lived */
      backdrop_url?: string;
      /** @description Section-specific badges (new, returning, …) */
      badges?: string[];
      /**
       * @description Deterministic catalog identifier
       * @example movie:heat-1995
       */
      content_id: string;
      /** @example R */
      content_rating?: string;
      /** Format: double */
      duration_seconds?: number;
      /** Format: int64 */
      episode_number?: number;
      /** @description Empty, never null */
      genres: string[];
      /** @description On a continue-watching card: in_progress or next_up */
      item_source?: string;
      /** @description Empty, never null */
      keywords: string[];
      /** @description Calendar date, YYYY-MM-DD */
      last_air_date?: string;
      /** @description Presigned, short-lived */
      logo_url?: string;
      /** Format: int64 */
      manga_chapter_count?: number;
      /** Format: int64 */
      manga_volume_count?: number;
      networks?: string[];
      /** @example en */
      original_language?: string;
      /** @description Technical badges of the best file */
      overlay_summary?: components["schemas"]["CatalogItemOverlay"];
      overview?: string;
      /** @description The item to play when the card is a series or season; absent when the item plays itself */
      play_content_id?: string;
      /**
       * Format: double
       * @description Resume position on a continue-watching card
       */
      position_seconds?: number;
      poster_thumbhash?: string;
      /** @description Presigned, short-lived */
      poster_url?: string;
      /**
       * Format: date-time
       * @description RFC 3339 instant in UTC with millisecond precision
       */
      progress_updated_at?: string;
      /** Format: double */
      rating_imdb?: number;
      /** Format: int64 */
      rating_rt_audience?: number;
      /** Format: int64 */
      rating_rt_critic?: number;
      /** Format: double */
      rating_tmdb?: number;
      /**
       * @description Calendar date, YYYY-MM-DD
       * @example 1995-12-15
       */
      release_date?: string;
      /**
       * Format: int64
       * @description Minutes
       * @example 170
       */
      runtime?: number;
      /** Format: int64 */
      season_number?: number;
      /** @description Owning series of an episode or season */
      series_id?: string;
      series_title?: string;
      /** @description Airing state of a series */
      show_status?: string;
      /** @description The values the listing was sorted by */
      sort_metrics?: components["schemas"]["CatalogItemSortMetrics"];
      /**
       * @description Metadata match state of the item
       * @example matched
       */
      status: string;
      studios?: string[];
      /** @example Heat */
      title: string;
      /**
       * @description movie, series, season, episode, audiobook, ebook, podcast, podcast_episode
       * @example movie
       */
      type: string;
      upcoming_event?: components["schemas"]["CatalogItemUpcomingEvent"];
      /** @description The viewer's flags; absent without a profile */
      user_state?: components["schemas"]["CatalogItemUserState"];
      /**
       * @description Which pool the card came from
       * @example recommendation
       * @enum {string}
       */
      watch_tonight_source: "continue_watching" | "next_up" | "recommendation";
      /** @description Sibling editions of the same work */
      work_formats?: components["schemas"]["CatalogWorkFormat"][];
      /** @description The work (book) an audiobook or ebook edition belongs to */
      work_id?: string;
      work_title?: string;
      /**
       * Format: int64
       * @example 1995
       */
      year?: number;
    };
    WatchUserData: {
      /**
       * Format: double
       * @example 10200
       */
      duration_seconds?: number;
      /** Format: int64 */
      in_progress_count: number;
      is_in_progress?: boolean;
      last_codec_video?: string;
      last_edition_key?: string;
      /**
       * @description The version last played
       * @example 1
       */
      last_file_id?: string;
      last_hdr?: boolean;
      last_resolution?: string;
      played: boolean;
      /**
       * Format: double
       * @example 1325.5
       */
      position_seconds?: number;
      /** Format: int64 */
      unplayed_count: number;
      /** Format: int64 */
      watched_count: number;
    };
    WatchVideoTrack: {
      aspect_ratio?: string;
      /** Format: int64 */
      bit_depth?: number;
      /** Format: int64 */
      bitrate?: number;
      /** @example hevc */
      codec?: string;
      color_primaries?: string;
      color_range?: string;
      color_space?: string;
      color_transfer?: string;
      dolby_vision?: string;
      /** Format: int64 */
      dv_bl_compat_id?: number;
      dv_bl_compat_id_present: boolean;
      dv_bl_present?: boolean;
      dv_config_present: boolean;
      dv_el_present?: boolean;
      /** @description none, mel, fel, unknown */
      dv_enhancement_layer?: string;
      /** Format: int64 */
      dv_level?: number;
      /** Format: int64 */
      dv_profile?: number;
      dv_rpu_present?: boolean;
      frame_rate?: string;
      hdr10_plus?: boolean;
      /** Format: int64 */
      height?: number;
      interlaced: boolean;
      /** Format: int64 */
      level?: number;
      pixel_format?: string;
      profile?: string;
      /** Format: int64 */
      reference_frames?: number;
      title?: string;
      video_range?: string;
      video_range_type?: string;
      /** Format: int64 */
      width?: number;
    };
  };
  responses: never;
  parameters: never;
  requestBodies: never;
  headers: never;
  pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
  getCurrentUser: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["Account"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listAdminUsers: {
    parameters: {
      query?: {
        /** @description Opaque cursor from page.next_cursor */
        cursor?: string;
        /** @description Page size; default 50, maximum 200 */
        limit?: number;
      };
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["AdminUserCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getAudioPreference: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The series content id */
        series_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["AudioPreference"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  updateAudioPreference: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The series content id */
        series_id: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["AudioPreferenceUpdate"];
      };
    };
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  deleteAudioPreference: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The series content id */
        series_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getCalendar: {
    parameters: {
      query: {
        /** @description Last local day of the window, YYYY-MM-DD; at most 30 days after start */
        end: string;
        /** @description Which items to include; absent is all */
        filter?:
          | "all"
          | "everything"
          | "following"
          | "favorites"
          | "watchlist"
          | "popular"
          | "trending";
        /** @description Restrict to one library */
        library_id?: string;
        /** @description First local day of the window, YYYY-MM-DD */
        start: string;
        /** @description IANA zone the days are reckoned in; absent is UTC */
        timezone?: string;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["Calendar"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listFavorites: {
    parameters: {
      query?: {
        /** @description Opaque cursor from page.next_cursor */
        cursor?: string;
        /** @description Artwork variant to presign; absent picks each surface's default */
        image_size?: "small" | "medium" | "large" | "original";
        /** @description Page size; default 50, maximum 200 */
        limit?: number;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["FavoriteCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getFavorite: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Catalog item identifier */
        item_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["FavoriteEntry"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  addFavorite: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Catalog item identifier */
        item_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  deleteFavorite: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Catalog item identifier */
        item_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listHistory: {
    parameters: {
      query?: {
        /** @description Opaque cursor from page.next_cursor */
        cursor?: string;
        /** @description Artwork variant to presign; absent picks each surface's default */
        image_size?: "small" | "medium" | "large" | "original";
        /** @description Page size; default 50, maximum 200 */
        limit?: number;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["HistoryCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  removeHistoryEntries: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["HistoryRemoveInputBody"];
      };
    };
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  dismissHomeItem: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Content identifier of the card */
        item_id: string;
        /** @description The home surface the item is hidden from */
        surface: "continue_watching" | "next_up";
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["HomeDismissal"];
      };
    };
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  undismissHomeItem: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Content identifier of the card */
        item_id: string;
        /** @description The home surface the item is hidden from */
        surface: "continue_watching" | "next_up";
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getHomeLayout: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["SectionLayout"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listHomeSections: {
    parameters: {
      query?: {
        /** @description Artwork variant to presign; absent picks each surface's default */
        image_size?: "small" | "medium" | "large" | "original";
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["SectionCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getHomeSectionItems: {
    parameters: {
      query?: {
        /** @description Artwork variant to presign; absent picks each surface's default */
        image_size?: "small" | "medium" | "large" | "original";
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Section identifier from the home layout */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["Section"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listLibraries: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["LibraryCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  createLibrary: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["LibraryCreate"];
      };
    };
    responses: {
      /** @description Created */
      201: {
        headers: {
          Location?: string;
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["Library"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  deleteLibrary: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The library */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description Accepted */
      202: {
        headers: {
          Location?: string;
          "Retry-After"?: string;
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["AdminJob"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  updateLibrary: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The library to update */
        id: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["LibraryUpdate"];
      };
    };
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["Library"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  checkLibraryMount: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The library */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["LibraryMountCheck"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  confirmEmptyRootCleanup: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The library */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["EmptyRootCleanup"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getMetadataMatchQueue: {
    parameters: {
      query?: {
        /** @description Opaque cursor from the previous page */
        cursor?: string;
        /** @description Rows per list; default 10, maximum 50 */
        limit?: number;
      };
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Library identifier */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["MetadataMatchQueueDetail"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  cancelMetadataMatchQueue: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The library */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["MetadataMatchQueueAction"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  retryMetadataMatchQueue: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The library */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["MetadataMatchQueueAction"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  uploadLibraryPoster: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Library identifier */
        id: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        "multipart/form-data": components["schemas"]["LibraryPosterForm"];
      };
    };
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["Library"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  deleteLibraryPoster: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The library */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getLibraryProviders: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The library */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["LibraryProviders"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  setLibraryProviders: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Library identifier */
        id: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["LibraryProvidersSet"];
      };
    };
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  refreshLibraryMetadata: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Library identifier */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: {
      content: {
        "application/json": components["schemas"]["LibraryRefresh"];
      };
    };
    responses: {
      /** @description Accepted */
      202: {
        headers: {
          Location?: string;
          "Retry-After"?: string;
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["AdminJob"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listMetadataMatchQueues: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["MetadataMatchQueueStatusCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getLibraryProviderDefaults: {
    parameters: {
      query: {
        /** @description Library kind to seed for */
        library_type: string;
      };
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["LibraryProviderDefaults"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  reorderLibraries: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["LibraryReorder"];
      };
    };
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listLibraryRoots: {
    parameters: {
      query: {
        /** @description Opaque cursor from page.next_cursor */
        cursor?: string;
        /** @description The library whose roots to list */
        library_id: string;
        /** @description Page size; default 50, maximum 200 */
        limit?: number;
        /** @description Case-insensitive substring over root path, title and sample file path */
        q?: string;
        /** @description Only roots in this inference state */
        state?: string;
      };
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["LibraryRootCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  setRootOverride: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["RootOverrideSet"];
      };
    };
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  deleteRootOverride: {
    parameters: {
      query: {
        /** @description Opaque identifier */
        library_id: string;
        /** @description The root, as listLibraryRoots reports it */
        root_path: string;
      };
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listSkippedRoots: {
    parameters: {
      query?: {
        /** @description Opaque cursor from page.next_cursor */
        cursor?: string;
        /** @description Page size; default 50, maximum 200 */
        limit?: number;
        /** @description Substring over root path, library name or reason */
        q?: string;
      };
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["SkippedRootCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listStaleIds: {
    parameters: {
      query?: {
        /** @description Opaque cursor from page.next_cursor */
        cursor?: string;
        /** @description Page size; default 50, maximum 200 */
        limit?: number;
        /** @description Substring over title, provider, provider ID or library name */
        q?: string;
      };
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["StaleMediaIDCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  rematchStaleId: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The catalog item */
        content_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listUnmatchedItems: {
    parameters: {
      query?: {
        /** @description Opaque cursor from page.next_cursor */
        cursor?: string;
        /** @description Page size; default 50, maximum 200 */
        limit?: number;
        /** @description Case-insensitive substring over title, type, status and library name */
        q?: string;
      };
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["UnmatchedItemCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getLibraryJob: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional first precondition, evaluated before If-None-Match: a tag that does not match the current representation is 412 precondition_failed. */
        "If-Match"?: string;
        "If-None-Match"?: string;
      };
      path: {
        job_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          /** @description The strong, opaque validator of the representation; send it back in If-Match on a guarded mutation or If-None-Match on a conditional read. */
          ETag?: string;
          Location?: string;
          "Retry-After"?: string;
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["AdminJob"];
        };
      };
      /** @description The representation named by If-None-Match is current; no body. */
      304: {
        headers: {
          /** @description The strong, opaque validator of the representation; send it back in If-Match on a guarded mutation or If-None-Match on a conditional read. */
          ETag?: string;
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Precondition Failed */
      412: {
        headers: {
          /** @description The strong, opaque validator of the representation; send it back in If-Match on a guarded mutation or If-None-Match on a conditional read. */
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  cancelLibraryJob: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name the authenticated account's primary profile; an absent header is accepted. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        job_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description The job was already canceled. */
      200: {
        headers: {
          ETag?: string;
          Location?: string;
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["AdminJob"];
        };
      };
      /** @description Accepted */
      202: {
        headers: {
          ETag?: string;
          Location?: string;
          "Retry-After"?: string;
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["AdminJob"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listLibraryPlaybackPreferences: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["LibraryPlaybackPreferenceCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  deleteLibraryPlaybackPreference: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The library */
        library_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  updateLibraryPlaybackPreference: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The library */
        library_id: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["LibraryPlaybackPreferenceUpdate"];
      };
    };
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getLibraryCollections: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Library identifier */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["LibraryCollectionTab"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getLibraryLayout: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Library identifier */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["SectionLayout"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listLibrarySections: {
    parameters: {
      query?: {
        /** @description Artwork variant to presign; absent picks each surface's default */
        image_size?: "small" | "medium" | "large" | "original";
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Library identifier */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["SectionCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getLibrarySectionItems: {
    parameters: {
      query?: {
        /** @description Artwork variant to presign; absent picks each surface's default */
        image_size?: "small" | "medium" | "large" | "original";
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Library identifier */
        id: string;
        /** @description Section identifier from the layout */
        section_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["Section"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listLibraryUserCollections: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Library identifier */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["UserCollectionCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getOpenAPIDocument: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description The OpenAPI 3.1 document, exactly the committed contracts/api/v2/openapi.json bytes. */
      200: {
        headers: {
          "Cache-Control"?: string;
          "Content-Type"?: string;
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          "application/json": {
            [key: string]: unknown;
          };
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listProfileSectionOverrides: {
    parameters: {
      query?: {
        /** @description The library; required when scope is library and refused otherwise */
        library_id?: string;
        /** @description The page the overrides apply to */
        scope?: "home" | "library";
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["SectionOverrideCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  replaceProfileSectionOverrides: {
    parameters: {
      query?: {
        /** @description The library; required when scope is library and refused otherwise */
        library_id?: string;
        /** @description The page the overrides apply to */
        scope?: "home" | "library";
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["SectionOverrideSet"];
      };
    };
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  resetProfileSectionOverrides: {
    parameters: {
      query?: {
        /** @description The library; required when scope is library and refused otherwise */
        library_id?: string;
        /** @description The page the overrides apply to */
        scope?: "home" | "library";
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getProfileSectionFlags: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["ProfileSectionFlags"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getProfileSectionSettings: {
    parameters: {
      query?: {
        /** @description The library; required when scope is library and refused otherwise */
        library_id?: string;
        /** @description The page the overrides apply to */
        scope?: "home" | "library";
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["ProfileSectionSettingCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listProfiles: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name a profile of the authenticated account. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["ProfileCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  createProfile: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name a profile of the authenticated account. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["ProfileCreate"];
      };
    };
    responses: {
      /** @description Created */
      201: {
        headers: {
          Location?: string;
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["Profile"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  deleteProfile: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name a profile of the authenticated account. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The profile */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  updateProfile: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name a profile of the authenticated account. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The profile to update */
        id: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["ProfileUpdate"];
      };
    };
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["Profile"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  uploadProfileAvatar: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name a profile of the authenticated account. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The profile */
        id: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        "multipart/form-data": components["schemas"]["ProfileAvatarForm"];
      };
    };
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["Profile"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  deleteProfileAvatar: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name a profile of the authenticated account. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The profile */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  verifyProfilePIN: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name a profile of the authenticated account. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The profile whose PIN is checked */
        id: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["ProfilePINCheck"];
      };
    };
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["ProfileVerification"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listHouseholdSessions: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name a profile of the authenticated account. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["PlaybackSessionCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listProgress: {
    parameters: {
      query?: {
        /** @description Opaque cursor from page.next_cursor */
        cursor?: string;
        /** @description Only entries whose item is in this library */
        library_id?: string;
        /** @description Page size; default 50, maximum 200 */
        limit?: number;
        /** @description Only entries in this state; absent lists every entry */
        status?: "in_progress" | "completed";
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["ProgressCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listRatings: {
    parameters: {
      query?: {
        /** @description Opaque cursor from page.next_cursor */
        cursor?: string;
        /** @description Page size; default 50, maximum 200 */
        limit?: number;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["RatingCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getRating: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Catalog item identifier */
        item_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["RatingEntry"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  setRating: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Catalog item identifier */
        item_id: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["RatingSet"];
      };
    };
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  deleteRating: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Catalog item identifier */
        item_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listBecauseWatched: {
    parameters: {
      query?: {
        /** @description Cards to answer; default 20, maximum 50 */
        limit?: number;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The watched item recommendations are drawn from */
        item_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["CatalogItemCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getDiscover: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["RecommendationRowCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getForYouMain: {
    parameters: {
      query?: {
        /** @description Cards to answer; default 20, maximum 50 */
        limit?: number;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["RecommendationRow"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listForYouRows: {
    parameters: {
      query?: {
        /** @description Cards to answer; default 20, maximum 50 */
        limit?: number;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["RecommendationRowCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listPopular: {
    parameters: {
      query?: {
        /** @description Window in days; default 30 */
        days?: number;
        /** @description Cards to answer; default 20, maximum 50 */
        limit?: number;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["CatalogItemCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listRecentlyAdded: {
    parameters: {
      query?: {
        /** @description Window in days; default 14 */
        days?: number;
        /** @description Cards to answer; default 20, maximum 50 */
        limit?: number;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["CatalogItemCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getRecommendationSection: {
    parameters: {
      query?: {
        /** @description Section key: the cluster index of a cluster section or the genre name of a genre section */
        key?: string;
        /** @description Cards to answer; default and maximum 60 */
        limit?: number;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Section kind, as a discover row's kind names it */
        kind:
          | "for-you-main"
          | "cluster"
          | "similar-users"
          | "popular"
          | "recently-added"
          | "top-rated"
          | "genre";
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["RecommendationRow"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listSimilarUsersLiked: {
    parameters: {
      query?: {
        /** @description Cards to answer; default 20, maximum 50 */
        limit?: number;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["CatalogItemCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listSimilar: {
    parameters: {
      query?: {
        /** @description Cards to answer; default 20, maximum 50 */
        limit?: number;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The item similar items are drawn from */
        item_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["CatalogItemCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getTasteProfile: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["TasteProfile"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  createTasteSeed: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["TasteSeedSubmission"];
      };
    };
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["TasteSeedResult"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listTasteSeedItems: {
    parameters: {
      query?: {
        /** @description Opaque cursor from page.next_cursor */
        cursor?: string;
        /** @description Cards per page; default 30, maximum 60 */
        limit?: number;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["CatalogItemCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getWatchTonight: {
    parameters: {
      query?: {
        /** @description Cards to answer; default 5, maximum 20 */
        limit?: number;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["WatchTonight"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listWatchTonightCards: {
    parameters: {
      query: {
        /** @description Catalog identifiers already swiped, repeated (exclude_ids=a&exclude_ids=b) */
        exclude_ids?: string[];
        /** @description Discover-mode genre filter, repeated (genres=Crime&genres=Thriller); each must be a known genre */
        genres?: string[];
        /** @description Cards per page; default 12, maximum 20 */
        limit?: number;
        /** @description continue answers in-progress and next-up cards; discover answers taste candidates */
        mode: "continue" | "discover";
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["WatchTonightCardPage"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listSectionRecipes: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name a profile of the authenticated account. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["RecipeCatalog"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listSectionRecipeCandidates: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name a profile of the authenticated account. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Recipe type from the catalog */
        type: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["RecipeCandidateCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getSettingsContract: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name a profile of the authenticated account. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description The public settings manifest, exactly the canonical bytes of contracts/settings/v1 with maintainer-only fields removed; the same document v1 /settings/manifest serves. */
      200: {
        headers: {
          "Cache-Control"?: string;
          "Content-Type"?: string;
          ETag?: string;
          [name: string]: unknown;
        };
        content: {
          "application/json": {
            [key: string]: unknown;
          };
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getSettingsContractCapabilities: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name a profile of the authenticated account. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["SettingsContractCapabilities"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  updateSubtitleAppearanceDeviceOverride: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
        /** @description The client's stable device identifier */
        "X-Silo-Device-Id": string;
        /** @description Optional display name recorded on the device registry */
        "X-Silo-Device-Name"?: string;
        /** @description Optional platform recorded on the device registry */
        "X-Silo-Device-Platform"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["SubtitleAppearanceDeviceOverride"];
      };
    };
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["EffectiveSubtitleAppearance"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  deleteSubtitleAppearanceDeviceOverride: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
        /** @description The client's stable device identifier */
        "X-Silo-Device-Id": string;
        /** @description Optional display name recorded on the device registry */
        "X-Silo-Device-Name"?: string;
        /** @description Optional platform recorded on the device registry */
        "X-Silo-Device-Platform"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getOverlayConfig: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name a profile of the authenticated account. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          "Cache-Control"?: string;
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["OverlayConfig"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listPluginSettings: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name a profile of the authenticated account. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["PluginSettingsInstallationCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getPluginSettings: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name a profile of the authenticated account. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The plugin installation */
        installation_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["PluginSettings"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  updatePluginSettings: {
    parameters: {
      query?: never;
      header?: {
        /** @description Optional. When present, it must name a profile of the authenticated account. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The plugin installation */
        installation_id: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["PluginSettingsWrite"];
      };
    };
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["PluginSettings"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getEffectiveSubtitleAppearance: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
        /** @description The client's stable device identifier; absent resolves the profile-wide value */
        "X-Silo-Device-Id"?: string;
        /** @description Optional display name recorded on the device registry */
        "X-Silo-Device-Name"?: string;
        /** @description Optional platform recorded on the device registry */
        "X-Silo-Device-Platform"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["EffectiveSubtitleAppearance"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listSettingValues: {
    parameters: {
      query: {
        /** @description Another registered device of the profile whose profile_device value to address; absent means the declared device */
        device_id?: string;
        /** @description The setting keys to read, one keys parameter per key */
        keys: string[];
        /** @description The library a profile_library value belongs to */
        library_id?: string;
        /** @description Another profile on the account to act for; only the household parent may name one */
        profile_id?: string;
        /** @description The scope the value is stored at */
        scope:
          | "account"
          | "profile"
          | "profile_client"
          | "profile_device"
          | "profile_library"
          | "profile_series";
        /** @description The series a profile_series value belongs to */
        series_id?: string;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
        /** @description The client family a profile_client value belongs to */
        "X-Silo-Client-Family"?: "tv" | "mobile" | "tablet" | "desktop" | "web";
        /** @description The client's stable device identifier; the profile_device scope stores against it when device_id is absent */
        "X-Silo-Device-Id"?: string;
        /** @description Optional display name recorded on the device registry */
        "X-Silo-Device-Name"?: string;
        /** @description Optional platform recorded on the device registry */
        "X-Silo-Device-Platform"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["SettingValueCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getSettingValue: {
    parameters: {
      query: {
        /** @description Another registered device of the profile whose profile_device value to address; absent means the declared device */
        device_id?: string;
        /** @description The library a profile_library value belongs to */
        library_id?: string;
        /** @description Another profile on the account to act for; only the household parent may name one */
        profile_id?: string;
        /** @description The scope the value is stored at */
        scope:
          | "account"
          | "profile"
          | "profile_client"
          | "profile_device"
          | "profile_library"
          | "profile_series";
        /** @description The series a profile_series value belongs to */
        series_id?: string;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
        /** @description The client family a profile_client value belongs to */
        "X-Silo-Client-Family"?: "tv" | "mobile" | "tablet" | "desktop" | "web";
        /** @description The client's stable device identifier; the profile_device scope stores against it when device_id is absent */
        "X-Silo-Device-Id"?: string;
        /** @description Optional display name recorded on the device registry */
        "X-Silo-Device-Name"?: string;
        /** @description Optional platform recorded on the device registry */
        "X-Silo-Device-Platform"?: string;
      };
      path: {
        /** @description The setting key, as defined in the settings contract */
        key: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["SettingValue"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  updateSettingValue: {
    parameters: {
      query: {
        /** @description Another registered device of the profile whose profile_device value to address; absent means the declared device */
        device_id?: string;
        /** @description The library a profile_library value belongs to */
        library_id?: string;
        /** @description Another profile on the account to act for; only the household parent may name one */
        profile_id?: string;
        /** @description The scope the value is stored at */
        scope:
          | "account"
          | "profile"
          | "profile_client"
          | "profile_device"
          | "profile_library"
          | "profile_series";
        /** @description The series a profile_series value belongs to */
        series_id?: string;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
        /** @description The client family a profile_client value belongs to */
        "X-Silo-Client-Family"?: "tv" | "mobile" | "tablet" | "desktop" | "web";
        /** @description The client's stable device identifier; the profile_device scope stores against it when device_id is absent */
        "X-Silo-Device-Id"?: string;
        /** @description Optional display name recorded on the device registry */
        "X-Silo-Device-Name"?: string;
        /** @description Optional platform recorded on the device registry */
        "X-Silo-Device-Platform"?: string;
      };
      path: {
        /** @description The setting key, as defined in the settings contract */
        key: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["SettingValueWrite"];
      };
    };
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["SettingValue"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  deleteSettingValue: {
    parameters: {
      query: {
        /** @description Another registered device of the profile whose profile_device value to address; absent means the declared device */
        device_id?: string;
        /** @description The library a profile_library value belongs to */
        library_id?: string;
        /** @description Another profile on the account to act for; only the household parent may name one */
        profile_id?: string;
        /** @description The scope the value is stored at */
        scope:
          | "account"
          | "profile"
          | "profile_client"
          | "profile_device"
          | "profile_library"
          | "profile_series";
        /** @description The series a profile_series value belongs to */
        series_id?: string;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
        /** @description The client family a profile_client value belongs to */
        "X-Silo-Client-Family"?: "tv" | "mobile" | "tablet" | "desktop" | "web";
        /** @description The client's stable device identifier; the profile_device scope stores against it when device_id is absent */
        "X-Silo-Device-Id"?: string;
        /** @description Optional display name recorded on the device registry */
        "X-Silo-Device-Name"?: string;
        /** @description Optional platform recorded on the device registry */
        "X-Silo-Device-Platform"?: string;
      };
      path: {
        /** @description The setting key, as defined in the settings contract */
        key: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listEffectiveSettings: {
    parameters: {
      query?: {
        /** @description Another registered device of the profile to resolve for; absent means the declared device */
        device_id?: string;
        /** @description The setting keys to resolve, one keys parameter per key; absent resolves every server-stored setting */
        keys?: string[];
        /** @description Libraries whose profile_library values take part, one library_ids parameter per id */
        library_ids?: string[];
        /** @description Another profile on the account to resolve for; only the household parent may name one */
        profile_id?: string;
        /** @description Series whose profile_series values take part, one series_ids parameter per id */
        series_ids?: string[];
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
        /** @description The client family whose profile_client values take part; required when a requested key has that scope */
        "X-Silo-Client-Family"?: "tv" | "mobile" | "tablet" | "desktop" | "web";
        /** @description The client's stable device identifier; its profile_device values take part */
        "X-Silo-Device-Id"?: string;
        /** @description Optional display name recorded on the device registry */
        "X-Silo-Device-Name"?: string;
        /** @description Optional platform recorded on the device registry */
        "X-Silo-Device-Platform"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["EffectiveSettingCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  resolveEffectiveSettings: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
        /** @description The client family whose profile_client values take part; required when a requested key has that scope */
        "X-Silo-Client-Family"?: "tv" | "mobile" | "tablet" | "desktop" | "web";
        /** @description The client's stable device identifier; its profile_device values take part */
        "X-Silo-Device-Id"?: string;
        /** @description Optional display name recorded on the device registry */
        "X-Silo-Device-Name"?: string;
        /** @description Optional platform recorded on the device registry */
        "X-Silo-Device-Platform"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["EffectiveSettingsBatch"];
      };
    };
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["EffectiveSettingContextCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  updateNavigationShortcut: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["NavigationShortcutMutation"];
      };
    };
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["SettingValue"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description retryable conflict: concurrent shortcut updates exhausted the compare-and-set retries; retry the request */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getSubtitlePreference: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The series content id */
        series_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["SubtitlePreference"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  updateSubtitlePreference: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The series content id */
        series_id: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["SubtitlePreferenceUpdate"];
      };
    };
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  deleteSubtitlePreference: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description The series content id */
        series_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  syncProgress: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        "application/json": components["schemas"]["ProgressSyncInputBody"];
      };
    };
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["ProgressSyncOutputBody"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Timeout */
      408: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Request Entity Too Large */
      413: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unsupported Media Type */
      415: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getSystemInfo: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          "Cache-Control"?: string;
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["SystemInfo"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getSetupStatus: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["SetupStatus"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getWatchState: {
    parameters: {
      query?: {
        /** @description Prefer this file when the item has several versions */
        file_id?: string;
        /** @description Artwork variant to presign; absent picks each surface's default */
        image_size?: "small" | "medium" | "large" | "original";
        /** @description Present the item as a member of this library */
        library_id?: string;
      };
      header?: {
        /** @description Optional. When present, it must name a profile of the authenticated account. */
        "X-Profile-Id"?: string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
        /** @description The stable device identifier used to resolve playback preferences */
        "X-Silo-Device-Id"?: string;
      };
      path: {
        /** @description A movie, episode, audiobook or ebook; a series is not directly playable */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["WatchDetail"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  markWatched: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description A movie, ebook, episode, season or series; a season or series expands to its episodes */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  unmarkWatched: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description A movie, ebook, episode, season or series; a season or series expands to its episodes */
        id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  listWatchlist: {
    parameters: {
      query?: {
        /** @description Opaque cursor from page.next_cursor */
        cursor?: string;
        /** @description Artwork variant to presign; absent picks each surface's default */
        image_size?: "small" | "medium" | "large" | "original";
        /** @description Page size; default 50, maximum 200 */
        limit?: number;
      };
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["WatchlistCollection"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  getWatchlistEntry: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Catalog item identifier */
        item_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description OK */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/json": components["schemas"]["WatchlistEntry"];
        };
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  addToWatchlist: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Catalog item identifier */
        item_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
  deleteWatchlistEntry: {
    parameters: {
      query?: never;
      header: {
        /** @description The household profile acting for this request; it must belong to the authenticated account. */
        "X-Profile-Id": string;
        /** @description Verification proof for a PIN-locked profile, issued by POST /api/v1/profiles/{id}/verify-pin until that operation moves to v2; required only when the declared profile is locked */
        "X-Profile-Token"?: string;
      };
      path: {
        /** @description Catalog item identifier */
        item_id: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description No Content */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description Bad Request */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unauthorized */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Not Acceptable */
      406: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Unprocessable Entity */
      422: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Too Many Requests */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Internal Server Error */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
      /** @description Service Unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          "application/problem+json": components["schemas"]["Problem"];
        };
      };
    };
  };
}
