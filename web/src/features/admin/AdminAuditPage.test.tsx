import '@testing-library/jest-dom/vitest'

import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('./adminApi', async (original) => ({
  ...await original<typeof import('./adminApi')>(),
  useAdminAudit: () => ({ isPending: false, error: null, hasNextPage: true, isFetchingNextPage: false, fetchNextPage: vi.fn(), data: { data: [{ id: 1, action: 'admin.policy.update', target_type: 'instance', target_id: 1, metadata: {}, created_at: '2026-10-12T08:00:00Z' }] } }),
}))

import { AdminAuditPage } from './AdminAuditPage'

afterEach(cleanup)

describe('AdminAuditPage', () => {
  it('lists entries and offers load more', () => {
    render(<AdminAuditPage />)
    expect(screen.getByText('admin.policy.update')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Load more' })).toBeVisible()
  })
})
