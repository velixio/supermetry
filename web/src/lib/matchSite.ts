import type { Site } from '@/lib/me'

/** A site is addressed by slug, public id or numeric id in URLs. */
export function matchesSite(site: Site, requested?: string): boolean {
  return requested !== undefined && [site.slug, site.public_id, String(site.id)].includes(requested)
}

/** The canonical URL segment for a site: slug, then public id, then numeric id. */
export function siteSlug(site: Site): string {
  return site.slug ?? site.public_id ?? String(site.id)
}
