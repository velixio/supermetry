import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { SiteIcon } from './SiteIcon'

afterEach(cleanup)

describe('SiteIcon', () => {
  it('shows the site initial when no icon is available', () => {
    render(<SiteIcon name="Docs" size={24} />)

    expect(screen.getByText('D')).toBeVisible()
    expect(screen.queryByRole('img')).not.toBeInTheDocument()
  })

  it('loads remote icons without sending a referrer', () => {
    const { container } = render(<SiteIcon iconUrl="https://docs.example/icon.png" name="Docs" size={24} />)

    const icon = container.querySelector('img')
    expect(icon).not.toBeNull()
    expect(icon).toHaveAttribute('src', 'https://docs.example/icon.png')
    expect(icon).toHaveAttribute('referrerpolicy', 'no-referrer')
    expect(icon).toHaveAttribute('loading', 'lazy')
    expect(icon).toHaveAttribute('decoding', 'async')
    expect(icon).toHaveStyle({ width: '24px', height: '24px' })
  })

  it('falls back to the site initial when the remote icon fails', () => {
    const { container } = render(<SiteIcon iconUrl="https://docs.example/missing.png" name="Docs" size={24} />)

    fireEvent.error(container.querySelector('img')!)

    expect(screen.getByText('D')).toBeVisible()
    expect(screen.queryByRole('img')).not.toBeInTheDocument()
  })
})
