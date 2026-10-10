import { beforeEach, describe, expect, it, vi } from 'vitest'

import { privateRequest } from '@/features/settings/privateApi'
import {
  deleteOrganization,
  getDeletedOrganizations,
  getDeletedSites,
  renameOrganization,
  restoreOrganization,
  restoreSite,
} from './api'

vi.mock('@/features/settings/privateApi', () => ({ privateRequest: vi.fn() }))

beforeEach(() => vi.mocked(privateRequest).mockReset())

describe('lifecycle API', () => {
  it('uses the lifecycle route contract and organization scope', async () => {
    vi.mocked(privateRequest).mockResolvedValue(undefined)

    await getDeletedSites(4)
    await restoreSite(7, 4)
    await renameOrganization(4, 'New name')
    await deleteOrganization(4, { password: 'secret', totp_code: '123456' })
    await getDeletedOrganizations()
    await restoreOrganization(4)

    expect(privateRequest).toHaveBeenNthCalledWith(1, 'GET', '/api/private/orgs/4/sites/deleted', { orgId: 4 })
    expect(privateRequest).toHaveBeenNthCalledWith(2, 'POST', '/api/private/sites/7/restore', { orgId: 4 })
    expect(privateRequest).toHaveBeenNthCalledWith(3, 'PATCH', '/api/private/orgs/4', { orgId: 4, body: { name: 'New name' } })
    expect(privateRequest).toHaveBeenNthCalledWith(4, 'DELETE', '/api/private/orgs/4', { orgId: 4, body: { password: 'secret', totp_code: '123456' } })
    expect(privateRequest).toHaveBeenNthCalledWith(5, 'GET', '/api/private/me/orgs/deleted')
    expect(privateRequest).toHaveBeenNthCalledWith(6, 'POST', '/api/private/me/orgs/4/restore')
  })
})
