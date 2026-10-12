import type { MessageTree } from './catalogue'
import type { LocaleCode } from './intl'

/** Translated messages per catalogue name: "main" (messages/en.ts) and lazily loaded ones such as "dashEdit". */
export type LocaleOverlay = Record<string, MessageTree>

export type LocaleInfo = {
  code: LocaleCode
  label: string
  nativeName: string
  /** False until a native speaker has reviewed the machine-assisted first translation. */
  reviewed: boolean
  pseudo?: boolean
  load: () => Promise<LocaleOverlay>
}

export const manifest: Record<LocaleCode, LocaleInfo> = {
  en: { code: 'en', label: 'English', nativeName: 'English', reviewed: true, load: async () => ({}) },
  'en-XA': { code: 'en-XA', label: 'Pseudo-locale (longer text)', nativeName: 'Ƥśéüđö', reviewed: false, pseudo: true, load: async () => ({}) },
  es: {
    code: 'es', label: 'Spanish', nativeName: 'Español', reviewed: false,
    load: async () => {
      const [main, dashEdit, ui, uiAccount] = await Promise.all([
        import('@/messages/locales/es'), import('@/messages/locales/es.dashEdit'), import('@/messages/locales/es.ui'), import('@/messages/locales/es.uiAccount'),
      ])
      return { main: main.default, dashEdit: dashEdit.default, ui: ui.default, uiAccount: uiAccount.default }
    },
  },
  de: {
    code: 'de', label: 'German', nativeName: 'Deutsch', reviewed: false,
    load: async () => {
      const [main, dashEdit, ui, uiAccount] = await Promise.all([
        import('@/messages/locales/de'), import('@/messages/locales/de.dashEdit'), import('@/messages/locales/de.ui'), import('@/messages/locales/de.uiAccount'),
      ])
      return { main: main.default, dashEdit: dashEdit.default, ui: ui.default, uiAccount: uiAccount.default }
    },
  },
}

/** Languages offered in the picker; the pseudo-locale is for testing only. */
export const pickerLocales: LocaleCode[] = ['en', 'es', 'de']
