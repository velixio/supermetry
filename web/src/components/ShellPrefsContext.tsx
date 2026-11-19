import { createContext, useContext } from 'react'

import type { Theme } from '@/lib/theme'

/** The shell's theme state, so the account menu and the command palette act on the same value. */
export type ShellPrefs = { theme: Theme; toggleTheme: () => void }

export const ShellPrefsContext = createContext<ShellPrefs | null>(null)

export const useShellPrefs = () => useContext(ShellPrefsContext)
