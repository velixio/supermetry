import { useMe } from "@/routes/$org";

export function useOrganization(orgSlug: string) {
  const me = useMe();
  return me.data?.memberships.find(
    (membership) => membership.org.slug === orgSlug,
  );
}

export function useSite(orgSlug: string, siteSlug: string) {
  const membership = useOrganization(orgSlug);
  const site = membership?.sites.find(
    (candidate) =>
      candidate.slug === siteSlug ||
      candidate.public_id === siteSlug ||
      String(candidate.id) === siteSlug,
  );
  return { membership, site };
}
