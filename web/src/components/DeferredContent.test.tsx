import '@testing-library/jest-dom/vitest'

import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { DeferredContent } from './DeferredContent'

afterEach(cleanup)

describe('DeferredContent', () => {
  it('reserves the final height until the content nears the viewport', () => {
    let intersect: IntersectionObserverCallback | undefined
    vi.stubGlobal('IntersectionObserver', class {
      constructor(callback: IntersectionObserverCallback) {
        intersect = callback
      }
      observe() {}
      unobserve() {}
      disconnect() {}
      takeRecords() { return [] }
      root = null
      rootMargin = ''
      thresholds = []
    })

    const { container } = render(
      <DeferredContent height={320} label="Breakdowns">
        <p>Loaded dashboard content</p>
      </DeferredContent>,
    )

    const region = screen.getByRole('region', { name: 'Breakdowns' })
    expect(region).toHaveStyle({ minHeight: '320px' })
    expect(screen.queryByText('Loaded dashboard content')).toBeNull()

    act(() => intersect?.([{ isIntersecting: true, intersectionRatio: 0.5 } as IntersectionObserverEntry], {} as IntersectionObserver))

    expect(screen.getByText('Loaded dashboard content')).toBeVisible()
    expect(container.firstChild).not.toHaveStyle({ minHeight: '320px' })
    vi.unstubAllGlobals()
  })
})
