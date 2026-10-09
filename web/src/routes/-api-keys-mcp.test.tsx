import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { McpConnectionGuide } from './app.$org.settings.api-keys'

afterEach(cleanup)

describe('MCP connection guide', () => {
  it('shows HTTP configurations with the endpoint and bearer header for supported clients', () => {
    render(<McpConnectionGuide />)

    expect(screen.getByText(/\/api\/public\/v1\/mcp/)).toBeVisible()
    expect(screen.getByText(/Authorization/)).toBeVisible()
    expect(screen.getByText(/Bearer <key>/)).toBeVisible()

    fireEvent.click(screen.getByRole('tab', { name: 'Cursor' }))
    expect(screen.getByRole('tabpanel')).toHaveTextContent('mcpServers')
    fireEvent.click(screen.getByRole('tab', { name: 'Generic' }))
    expect(screen.getByRole('tabpanel')).toHaveTextContent('type')
  })
})
