import '@testing-library/jest-dom/vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup } from '@testing-library/react'

import { JourneyFlow } from './JourneyFlow'
import { JourneyControls } from './JourneyControls'

afterEach(cleanup)

describe('JourneyFlow', () => {
  it('renders sankey-ready nodes, links, exits, and the other bucket accessibly', () => {
    render(
      <JourneyFlow
        nodes={[
          { step: 1, path: '/', visitors: 10 },
          { step: 2, path: '/pricing', visitors: 6 },
          { step: 2, path: 'Other', visitors: 2 },
          { step: 2, path: 'Exit', visitors: 2 },
        ]}
        links={[
          { from_step: 1, from_path: '/', to_path: '/pricing', visitors: 6 },
          { from_step: 1, from_path: '/', to_path: 'Other', visitors: 2 },
          { from_step: 1, from_path: '/', to_path: 'Exit', visitors: 2 },
        ]}
      />,
    )

    expect(screen.getByRole('img', { name: 'User journey path flow' })).toBeVisible()
    const table = screen.getByRole('table', { name: 'Journey links' })
    expect(table).toHaveTextContent('/ → /pricing')
    expect(table).toHaveTextContent('/ → Other')
    expect(table).toHaveTextContent('/ → Exit')
  })

})

describe('JourneyControls', () => {
  it('offers top pages for either direction and clamps steps to two through five', () => {
    const onChange = vi.fn()
    render(
      <JourneyControls
        direction="start"
        path=""
        steps={3}
        topPages={['/', '/pricing']}
        onChange={onChange}
      />,
    )

    fireEvent.change(screen.getByLabelText('Start page'), { target: { value: '/pricing' } })
    expect(onChange).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
    fireEvent.change(screen.getByLabelText('Journey direction'), { target: { value: 'end' } })
    fireEvent.change(screen.getByLabelText('Steps'), { target: { value: '5' } })

    expect(document.querySelector('datalist option[value="/pricing"]')).toBeInTheDocument()
    expect(onChange).toHaveBeenCalledWith({ direction: 'start', path: '/pricing', steps: 3 })
    expect(onChange).toHaveBeenCalledWith({ direction: 'end', path: '/pricing', steps: 3 })
    expect(onChange).toHaveBeenCalledWith({ direction: 'start', path: '/pricing', steps: 5 })
  })
})
