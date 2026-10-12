import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

const save = vi.fn()
const reset = vi.fn()
const saveSettings = vi.fn()
const templateData = { data: [{ kind: 'email', subject: 'Alert', body: 'Site {{.site_id}}', is_default: false }], settings: { history_retention_days: 30 }, variables: ['site_id', 'value'] }
vi.mock('./notificationTemplatesApi', () => ({
  useNotificationTemplates: () => ({ data: templateData, error: null }),
  useSaveNotificationTemplate: () => ({ mutate: save, isPending: false, error: null }),
  useResetNotificationTemplate: () => ({ mutate: reset, isPending: false, error: null }),
  useSaveNotificationSettings: () => ({ mutate: saveSettings, isPending: false, error: null }),
  previewNotificationTemplate: () => Promise.resolve({ subject: 'Alert', body: 'Site 7' }),
}))

import { NotificationTemplates } from './NotificationTemplates'

describe('NotificationTemplates', () => {
  afterEach(cleanup)
  it('documents variables, previews, saves and resets', async () => {
    render(<NotificationTemplates orgId={3} canManage />)
    expect(await screen.findByText('Site 7')).toBeVisible()
    expect(screen.getByText('{{.site_id}}')).toBeVisible()
    fireEvent.change(screen.getByLabelText('Message'), { target: { value: 'Value {{.value}}' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save template' }))
    expect(save).toHaveBeenCalledWith({ kind: 'email', input: { subject: 'Alert', body: 'Value {{.value}}' } })
    fireEvent.click(screen.getByRole('button', { name: 'Reset to default' }))
    expect(reset).toHaveBeenCalledWith('email')
    fireEvent.change(screen.getByLabelText('Keep delivery history'), { target: { value: '90' } })
    await waitFor(() => expect(saveSettings).toHaveBeenCalledWith({ history_retention_days: 90 }))
  })
})
