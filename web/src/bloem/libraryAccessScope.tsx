import { createContext, useContext, type ReactNode } from "react";

const LibraryAccessScopeContext = createContext<string | undefined>(undefined);

/**
 * Names whose libraries a LibraryAccessSelector below it offers: an
 * organization's, or (without a name) every library on the server.
 */
export function LibraryAccessScope({
  name,
  children,
}: {
  name: string | undefined;
  children: ReactNode;
}) {
  return (
    <LibraryAccessScopeContext.Provider value={name}>{children}</LibraryAccessScopeContext.Provider>
  );
}

// eslint-disable-next-line react-refresh/only-export-components -- the hook belongs with its provider
export function useLibraryAccessScope(): string | undefined {
  return useContext(LibraryAccessScopeContext);
}
