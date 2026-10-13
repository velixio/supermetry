import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { useLiveSummary, privateRequest, applyLocalePreference, toastError } = vi.hoisted(() => ({
  useLiveSummary: vi.fn(), privateRequest: vi.fn(), applyLocalePreference: vi.fn(), toastError: vi.fn(),
}))
vi.mock('@/features/realtime/useLiveSummary', () => ({ useLiveSummary }))
vi.mock('@/features/settings/privateApi', () => ({ privateRequest }))
vi.mock('sonner', () => ({ toast: { error: toastError } }))
vi.mock('@/lib/i18n', async (importOriginal) => ({ ...await importOriginal<typeof import('@/lib/i18n')>(), applyLocalePreference }))

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { pickerLocales } from '@/lib/i18n'
import { SidebarProvider } from '@/components/ui/sidebar'
import { TooltipProvider } from '@/components/ui/tooltip'
import { renderWithRouter } from '@/test/renderWithRouter'

import { AppHeader } from './AppHeader'

beforeEach(() => {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: () => ({ matches: false, addEventListener: () => undefined, removeEventListener: () => undefined }),
  })
})
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

function show(path: string, onOpenPalette = vi.fn(), locale: string = '') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  renderWithRouter(
    <QueryClientProvider client={client}>
    <TooltipProvider>
      <SidebarProvider>
        <AppHeader locale={locale as '' | 'en'} theme="light" onToggleTheme={vi.fn()} onOpenHelp={vi.fn()} onOpenPalette={onOpenPalette} />
      </SidebarProvider>
    </TooltipProvider>
    </QueryClientProvider>,
    path,
  )
  return screen.findByRole('banner', { name: 'Application header' })
}

describe('AppHeader outside an organization', () => {
  it('shows account crumbs and the header actions, without a live badge', async () => {
    const header = await show('/app/account/profile')
    const crumbs = within(header).getByRole('navigation', { name: 'Breadcrumb' })
    expect(within(crumbs).getByRole('link', { name: 'Account' })).toHaveAttribute('href', '/app/account')
    expect(within(crumbs).getByText('Profile')).toHaveAttribute('aria-current', 'page')
    expect(within(header).getByRole('button', { name: 'Show keyboard shortcuts' })).toBeInTheDocument()
    expect(within(header).getByRole('button', { name: 'Use dark theme' })).toBeInTheDocument()
    expect(useLiveSummary).not.toHaveBeenCalled()
  })

  it('opens the command palette from a labelled trigger that announces its shortcut', async () => {
    const onOpenPalette = vi.fn()
    const header = await show('/app/account/profile', onOpenPalette)
    const trigger = within(header).getByRole('button', { name: 'Open command palette' })
    expect(trigger).toHaveAttribute('aria-keyshortcuts', 'Control+K Meta+K')
    fireEvent.click(trigger)
    expect(onOpenPalette).toHaveBeenCalledOnce()
  })

  it('shows no breadcrumbs for an organization the user has no membership in', async () => {
    const header = await show('/app/missing')
    expect(within(header).queryByRole('navigation', { name: 'Breadcrumb' })).toBeNull()
  })
})

describe('AppHeader language menu', () => {
  const openLanguage = async (path: string, locale: string = '') => {
    const header = await show(path, vi.fn(), locale)
    const button = within(header).getByRole('button', { name: 'Language' })
    fireEvent.keyDown(button, { key: 'ArrowDown' })
    return { header, button }
  }

  it.each(['/app/account/profile', '/app/missing'])('has a language button before the theme button on %s', async (path) => {
    const header = await show(path)
    const buttons = within(header).getAllByRole('button')
    const names = buttons.map((b) => b.getAttribute('aria-label'))
    expect(names.indexOf('Language')).toBeGreaterThan(-1)
    expect(names.indexOf('Language')).toBe(names.indexOf('Use dark theme') - 1)
  })

  it('lists Browser default and every picker locale with the current one checked', async () => {
    await openLanguage('/app/account/profile', 'de')
    const items = await screen.findAllByRole('menuitemradio')
    expect(items).toHaveLength(pickerLocales.length + 1)
    expect(items[0]).toHaveAccessibleName('Browser default')
    expect(items[0]).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByRole('menuitemradio', { name: /^Deutsch/ })).toHaveAttribute('aria-checked', 'true')
  })

  it('checks Browser default when no locale is saved', async () => {
    await openLanguage('/app/account/profile')
    expect(await screen.findByRole('menuitemradio', { name: 'Browser default' })).toHaveAttribute('aria-checked', 'true')
  })

  it('does nothing when the already checked language is chosen again', async () => {
    await openLanguage('/app/account/profile', 'de')
    fireEvent.click(await screen.findByRole('menuitemradio', { name: /^Deutsch/ }))
    await new Promise((r) => setTimeout(r, 20))
    expect(privateRequest).not.toHaveBeenCalled()
    expect(applyLocalePreference).not.toHaveBeenCalled()
  })

  it('treats an unknown stored language as Browser default', async () => {
    await openLanguage('/app/account/profile', 'fr')
    expect(await screen.findByRole('menuitemradio', { name: 'Browser default' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getAllByRole('menuitemradio').filter((i) => i.getAttribute('aria-checked') === 'true')).toHaveLength(1)
  })

  it('saves the choice and applies it on success', async () => {
    privateRequest.mockResolvedValue({})
    await openLanguage('/app/account/profile')
    fireEvent.click(await screen.findByRole('menuitemradio', { name: /^Español/ }))
    await waitFor(() => expect(applyLocalePreference).toHaveBeenCalledWith('es'))
    expect(privateRequest).toHaveBeenCalledWith('PATCH', '/api/private/me', { body: { preferences: { locale: 'es' } } })
  })

  it('disables the trigger while saving', async () => {
    privateRequest.mockReturnValue(new Promise(() => undefined))
    const { button } = await openLanguage('/app/account/profile')
    fireEvent.click(await screen.findByRole('menuitemradio', { name: /^Español/ }))
    await waitFor(() => expect(button).toBeDisabled())
    expect(applyLocalePreference).not.toHaveBeenCalled()
  })

  it('shows a toast and keeps the language when saving fails', async () => {
    privateRequest.mockRejectedValue(new Error('boom'))
    await openLanguage('/app/account/profile')
    fireEvent.click(await screen.findByRole('menuitemradio', { name: /^Español/ }))
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('The language could not be saved. Try again.'))
    expect(applyLocalePreference).not.toHaveBeenCalled()
  })
})
