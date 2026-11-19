import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import { InfoTip } from './InfoTip'

afterEach(cleanup)

describe('InfoTip', () => {
  it('shows its text on tap and names the trigger', async () => {
    render(<TooltipProvider><InfoTip label="About bounce rate">Visits with one pageview.</InfoTip></TooltipProvider>)
    const trigger = screen.getByRole('button', { name: 'About bounce rate' })
    expect(screen.queryByRole('tooltip')).toBeNull()
    fireEvent.click(trigger)
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Visits with one pageview.')
  })
})
