import { createContext, useContext, type ReactNode } from 'react'

/**
 * Which organization's repositories a page shows, and whether the viewer may change them.
 * Owners and admins manage; every other member reads. The server enforces the same split.
 */
export type StarsOrg = { orgId: number; canManage: boolean }

const StarsOrgContext = createContext<StarsOrg | null>(null)

export function StarsOrgProvider({ value, children }: { value: StarsOrg; children: ReactNode }) {
  return <StarsOrgContext.Provider value={value}>{children}</StarsOrgContext.Provider>
}

export function useStarsOrg(): StarsOrg {
  const value = useContext(StarsOrgContext)
  if (!value) throw new Error('useStarsOrg must be used inside StarsOrgProvider')
  return value
}

export function canManageStars(role: string | undefined): boolean {
  return role === 'owner' || role === 'admin'
}
