import { createContext, useContext, type ReactNode } from "react";

import type { SupermetryClient } from "./client";

const SupermetryContext = createContext<SupermetryClient | null>(null);

export interface SupermetryProviderProps {
  /** A client from `createSupermetry()`; create it once, outside render. */
  client: SupermetryClient;
  children?: ReactNode;
}

export function SupermetryProvider({ client, children }: SupermetryProviderProps) {
  return <SupermetryContext.Provider value={client}>{children}</SupermetryContext.Provider>;
}

/** Returns the client from the nearest `<SupermetryProvider>`. */
export function useSupermetry(): SupermetryClient {
  const client = useContext(SupermetryContext);
  if (!client) throw new Error("useSupermetry() must be used inside <SupermetryProvider>");
  return client;
}
