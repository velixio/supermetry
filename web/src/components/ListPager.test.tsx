import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ListPager } from './ListPager'

afterEach(cleanup)

const handlers = {
  onFirst: vi.fn(),
  onPrevious: vi.fn(),
  onNext: vi.fn(),
}

describe('ListPager', () => {
  it('shows the exact range, total, and page count', () => {
    render(<ListPager {...handlers} fetching={false} hasNext page={2} pageSize={50} rowCount={50} total={1234} totalCapped={false} />)

    expect(screen.getByText('Showing 51–100 of 1,234 · Page 2 of 25')).toBeVisible()
  })

  it('shows capped totals and page counts without thousands separators', () => {
    render(<ListPager {...handlers} fetching={false} hasNext page={2} pageSize={50} rowCount={50} total={10000} totalCapped />)

    expect(screen.getByText('Showing 51–100 of 10000+ · Page 2 of 200+')).toBeVisible()
  })

  it('disables First and Previous on the first page', () => {
    render(<ListPager {...handlers} fetching={false} hasNext page={1} pageSize={50} rowCount={50} total={100} totalCapped={false} />)

    expect(screen.getByRole('button', { name: 'First' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Previous' })).toBeDisabled()
  })

  it('disables Next on the last page', () => {
    render(<ListPager {...handlers} fetching={false} hasNext={false} page={2} pageSize={50} rowCount={20} total={70} totalCapped={false} />)

    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled()
  })

  it('shows an empty state and disables every action', () => {
    render(<ListPager {...handlers} fetching={false} hasNext={false} page={1} pageSize={50} rowCount={0} total={0} totalCapped={false} />)

    expect(screen.getByText('No results')).toBeVisible()
    for (const name of ['First', 'Previous', 'Next']) {
      expect(screen.getByRole('button', { name })).toBeDisabled()
    }
  })
})
