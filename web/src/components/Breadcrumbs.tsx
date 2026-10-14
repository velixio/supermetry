import { Link, useRouterState, type LinkProps } from '@tanstack/react-router'
import { Fragment } from 'react'

import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from '@/components/ui/breadcrumb'
import type { Membership } from '@/lib/me'
import { ui } from '@/messages/ui'

import { buildCrumbs } from './routeCrumbs'

export function Breadcrumbs({ membership }: { membership?: Membership }) {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const crumbs = buildCrumbs(pathname, membership)
  if (crumbs.length === 0) return null

  return (
    <Breadcrumb aria-label={ui.crumbs.label} className="min-w-0">
      <BreadcrumbList className="flex-nowrap">
        {crumbs.map((crumb, index) => {
          const last = index === crumbs.length - 1
          // Below md only the last two crumbs show, so the header never overflows.
          const hiddenOnMobile = index < crumbs.length - 2
          const visibility = hiddenOnMobile ? 'hidden md:inline-flex' : ''
          return (
            <Fragment key={`${index}-${crumb.label}`}>
              {index > 0 ? <BreadcrumbSeparator className={index - 1 < crumbs.length - 2 ? 'hidden md:block' : ''} /> : null}
              <BreadcrumbItem className={`${visibility} min-w-0`} data-crumb>
                {last || !crumb.to ? (
                  <BreadcrumbPage className="truncate">{crumb.label}</BreadcrumbPage>
                ) : (
                  <BreadcrumbLink asChild>
                    {/* `to` is built from a fixed set of route templates in routeCrumbs.ts */}
                    <Link to={crumb.to as LinkProps['to']} params={crumb.params as never} className="truncate">
                      {crumb.label}
                    </Link>
                  </BreadcrumbLink>
                )}
              </BreadcrumbItem>
            </Fragment>
          )
        })}
      </BreadcrumbList>
    </Breadcrumb>
  )
}
