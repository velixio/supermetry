import { cloneMessages, mergeMessages, restoreMessages, type MessageTree } from './catalogue'
import { activeLocaleCode, isLocaleCode, setActiveLocaleCode, storageKey, type LocaleCode } from './intl'
import { manifest, type LocaleOverlay } from './manifest'
import { pseudoMessages } from './pseudo'

export { interpolate } from './interpolate'
export { activeLocale, activeLocaleCode, formatCurrency, formatDateTime, formatNumber, formatRelativeTime, isLocaleCode, weekStart } from './intl'
export type { LocaleCode } from './intl'
export { manifest, pickerLocales } from './manifest'

// Each message module registers its live catalogue object. Translations are laid
// over those objects in place, so no component has to change how it reads text.
const live = new Map<string, MessageTree>()
const english = new Map<string, MessageTree>()
let overlay: LocaleOverlay = {}
let pseudo = false

function apply(name: string): void {
  const tree = live.get(name)
  const base = english.get(name)
  if (!tree || !base) return
  restoreMessages(tree, base)
  if (pseudo) mergeMessages(tree, pseudoMessages(base))
  else if (overlay[name]) mergeMessages(tree, overlay[name])
}

/** Called by each message module with its English catalogue; applies the active language right away. */
export function registerCatalogue(name: string, tree: object): void {
  live.set(name, tree as MessageTree)
  if (!english.has(name)) english.set(name, cloneMessages(tree as MessageTree))
  apply(name)
}

/** Loads the active language's messages and applies them. Call once before the first render. */
export async function initI18n(): Promise<LocaleCode> {
  const code = activeLocaleCode()
  const info = manifest[code]
  pseudo = Boolean(info.pseudo)
  overlay = pseudo ? {} : await info.load()
  for (const name of live.keys()) apply(name)
  if (typeof document !== 'undefined') document.documentElement.lang = code === 'en-XA' ? 'en' : code
  return code
}

/** Switches the in-memory language (used by tests). Production switches by reloading. */
export async function switchLocale(code: LocaleCode): Promise<void> {
  setActiveLocaleCode(code)
  await initI18n()
}

export function storeLocalePreference(code: LocaleCode | ''): void {
  try {
    if (code === '') window.localStorage.removeItem(storageKey)
    else window.localStorage.setItem(storageKey, code)
  } catch {
    // Storage can be unavailable; the account preference still applies on the next sign-in.
  }
}

/** Remembers the choice and reloads, because a few labels are captured when modules load. */
export function applyLocalePreference(code: LocaleCode | ''): void {
  storeLocalePreference(code)
  window.location.reload()
}

/** Follows the account preference after sign-in; reloads at most once per session to avoid loops. */
export function syncAccountLocale(preference: string | undefined): void {
  if (preference === undefined) return
  const wanted = preference === '' ? undefined : isLocaleCode(preference) ? preference : undefined
  const stored = (() => { try { return window.localStorage.getItem(storageKey) } catch { return null } })()
  if ((wanted ?? null) === stored) return
  if (wanted === undefined && stored === null) return
  const flag = 'supermetry.locale.synced'
  try {
    if (window.sessionStorage.getItem(flag) === `${wanted ?? ''}`) return
    window.sessionStorage.setItem(flag, `${wanted ?? ''}`)
  } catch {
    return
  }
  applyLocalePreference(wanted ?? '')
}
