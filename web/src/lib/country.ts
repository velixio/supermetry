const names =
  typeof Intl !== 'undefined' && 'DisplayNames' in Intl
    ? new Intl.DisplayNames(['en'], { type: 'region' })
    : null

const isoCode = /^[A-Za-z]{2}$/

export function normalizeCountryCode(code: string): string | undefined {
  if (!isoCode.test(code)) return undefined
  return code.toUpperCase()
}

export function countryFlagSrc(code: string): string | undefined {
  return normalizeCountryCode(code) ? `/flags/${code.toLowerCase()}.svg` : undefined
}

export function countryName(code: string): string {
  if (!code) return 'Unknown'
  if (!isoCode.test(code)) return code
  try {
    const normalized = code.toUpperCase()
    const name = names?.of(normalized)
    return name && name.toUpperCase() !== normalized ? name : 'Unknown'
  } catch {
    return 'Unknown'
  }
}

export function countryLabel(code: string): string {
  return countryName(code)
}
