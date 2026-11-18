import { Link } from '@tanstack/react-router'

import { en } from '@/messages/en'

const t = en.linksUi
const tabClass =
  'inline-flex h-8 max-sm:min-h-11 items-center rounded-md px-3 text-sm font-medium text-muted-foreground hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none data-[status=active]:bg-accent data-[status=active]:text-foreground'

/** Section switcher shared by the short-link and tracking-pixel pages. */
export function LinksTabs({ org, site }: { org: string; site: string }) {
  return (
    <nav aria-label={t.tabsLabel} className="flex gap-1">
      <Link activeOptions={{ exact: true }} className={tabClass} params={{ org, site }} to="/app/$org/$site/links">
        {t.tabLinks}
      </Link>
      <Link className={tabClass} params={{ org, site }} to="/app/$org/$site/links/pixel">
        {t.tabPixel}
      </Link>
    </nav>
  )
}
