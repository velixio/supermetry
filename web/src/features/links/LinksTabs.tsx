import { Link } from '@tanstack/react-router'

import { en } from '@/messages/en'

const t = en.linksUi
const tabClass =
  'inline-flex min-h-11 items-center border-b-2 border-transparent px-3 text-sm font-medium text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring data-[status=active]:border-primary data-[status=active]:text-foreground'

/** Section switcher shared by the short-link and tracking-pixel pages. */
export function LinksTabs({ org, site }: { org: string; site: string }) {
  return (
    <nav aria-label={t.tabsLabel} className="flex gap-1 border-b">
      <Link activeOptions={{ exact: true }} className={tabClass} params={{ org, site }} to="/app/$org/$site/links">
        {t.tabLinks}
      </Link>
      <Link className={tabClass} params={{ org, site }} to="/app/$org/$site/links/pixel">
        {t.tabPixel}
      </Link>
    </nav>
  )
}
