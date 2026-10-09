import { render, type RenderOptions } from '@testing-library/react'
import type { ReactNode } from 'react'
import { AuthorizationProvider } from '@/lib/authorization'
import { renderWithRouter } from './renderWithRouter'

export const testCapabilities = [
  'profiles.read',
  'replay.read',
  'data.export',
  'billing.manage',
]

export function Authorized({ children }: { children: ReactNode }) {
  return (
    <AuthorizationProvider
      membership={{ effective_capabilities: testCapabilities }}
    >
      {children}
    </AuthorizationProvider>
  )
}

// Existing feature tests explicitly model an authorized user. Security tests use
// AuthorizationProvider directly with the exact server-granted capabilities.
export function renderAuthorized(
  children: ReactNode,
  options?: Omit<RenderOptions, 'wrapper'>,
) {
  return render(children, { ...options, wrapper: Authorized })
}

export function renderAuthorizedWithRouter(
  children: ReactNode,
  initialEntry = '/',
) {
  return renderWithRouter(<Authorized>{children}</Authorized>, initialEntry)
}
