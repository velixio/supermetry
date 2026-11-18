import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'
import { InvitationsPage } from './MembersPages'

describe('InvitationsPage focusInvite', () => {
  it('focuses the email field when opened from the invite action', () => {
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { enabled: false } } })}>
        <InvitationsPage orgId={1} actorRole="owner" focusInvite />
      </QueryClientProvider>,
    )
    expect(document.activeElement).toBe(screen.getByLabelText('Email'))
  })
})
