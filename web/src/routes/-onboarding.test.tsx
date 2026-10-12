import { describe, expect, it } from 'vitest'

import { Route } from './onboarding'

describe('/onboarding', () => {
  it('redirects legacy links to /app with replacement', () => {
    expect(() => Route.options.beforeLoad?.({} as never)).toThrow()
    try {
      Route.options.beforeLoad?.({} as never)
    } catch (error) {
      expect(error).toMatchObject({ options: { to: '/app', replace: true } })
    }
  })
})
