import { createContext, useContext, type ReactNode } from 'react'

export type Capability =
  'profiles.read' | 'replay.read' | 'data.export' | 'billing.manage'
export type Authorization = { effective_capabilities?: readonly string[] }

// The server computes role defaults and additive grants. Missing policy data denies
// access; the UI must never recreate role policy or grant itself a fallback.
export function hasCapability(
  membership: Authorization | undefined,
  capability: Capability,
): boolean {
  return membership?.effective_capabilities?.includes(capability) === true
}

const AuthorizationContext = createContext<Authorization | undefined>(undefined)

export function AuthorizationProvider({
  membership,
  children,
}: {
  membership?: Authorization
  children: ReactNode
}) {
  return (
    <AuthorizationContext.Provider value={membership}>
      {children}
    </AuthorizationContext.Provider>
  )
}

export function useCapability(capability: Capability): boolean {
  return hasCapability(useContext(AuthorizationContext), capability)
}

export function AccessDenied({ subject }: { subject: string }) {
  return (
    <p role="alert" className="text-sm text-muted-foreground">
      You don’t have permission to {subject}. Ask an organization owner or admin
      for access.
    </p>
  )
}
