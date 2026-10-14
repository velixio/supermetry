import { useLocation } from "@remix-run/react";

import { useSupermetryPageviews, type SupermetryProps } from "./router.js";

export * from "./router.js";

export function Supermetry(props: SupermetryProps): null {
  useSupermetryPageviews(useLocation(), props);
  return null;
}
