import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { BrowserLabel } from './BrowserLabel'
import { DeviceLabel } from './DeviceLabel'
import { Location } from './Location'
import { OsLabel } from './OsLabel'

afterEach(cleanup)

describe('visitor labels', () => {
  it('renders a full city and country without exposing the country code', () => {
    render(<Location city="Bengaluru" country="IN" />)
    expect(screen.getByText('Bengaluru, India')).toBeVisible()
    expect(screen.queryByText('IN')).not.toBeInTheDocument()
  })

  it('renders a full country name without a city', () => {
    render(<Location country="US" />)
    expect(screen.getByText('United States')).toBeVisible()
    expect(screen.queryByText('US')).not.toBeInTheDocument()
  })

  it('renders a globe and Unknown without a location', () => {
    render(<Location />)
    expect(screen.getByText('Unknown')).toBeVisible()
    expect(screen.getByTestId('location-unknown-icon')).toBeVisible()
  })

  it('uses Unknown for empty agent values and hides broken decorative images', () => {
    render(<><BrowserLabel value="" /><OsLabel value="" /><DeviceLabel value="" /></>)
    expect(screen.getAllByText('Unknown')).toHaveLength(3)

    cleanup()
    const { container } = render(<BrowserLabel value="Chrome" />)
    const image = screen.getByRole('presentation', { hidden: true })
    expect(image).toHaveAttribute('alt', '')
    expect(image).toHaveAttribute('aria-hidden', 'true')
    expect(image).toHaveAttribute('width', '16')
    expect(image).toHaveAttribute('height', '16')
    fireEvent.error(image)
    expect(container.querySelector('img')).not.toBeInTheDocument()
    expect(screen.queryByRole('presentation', { hidden: true })).not.toBeInTheDocument()
  })

  it('capitalizes device names', () => {
    render(<DeviceLabel value="mobile" />)
    expect(screen.getByText('Mobile')).toBeVisible()
  })
})
