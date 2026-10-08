import { describe, expect, it } from 'vitest'

import { router } from '@/router'

describe('replay routes', () => {
  // The list page renders no <Outlet />, so the player must not be its child,
  // or clicking ▶ changes the URL but keeps showing the list.
  it('mounts the player beside the replay list, not inside it', () => {
    const routes = router.routesById as unknown as Record<string, { parentRoute?: { id: string } }>
    expect(routes['/$org/$site/replays/$id']?.parentRoute?.id).toBe('/$org')
    expect(routes['/$org/$site/replays/']).toBeDefined()
  })
})
