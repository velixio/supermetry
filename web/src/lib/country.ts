const names =
  typeof Intl !== 'undefined' && 'DisplayNames' in Intl
    ? new Intl.DisplayNames(['en'], { type: 'region' })
    : null

const isoCode = /^[A-Za-z]{2}$/

// Flag emoji from the two regional-indicator code points; '' for non-ISO input.
export function countryFlag(code: string): string {
  if (!isoCode.test(code)) return ''
  return String.fromCodePoint(...[...code.toUpperCase()].map((c) => 0x1f1a5 + c.charCodeAt(0)))
}

export function countryName(code: string): string {
  if (!code) return 'Unknown'
  if (!isoCode.test(code)) return code
  try {
    return names?.of(code.toUpperCase()) ?? code
  } catch {
    return code
  }
}

export function countryLabel(code: string): string {
  const flag = countryFlag(code)
  const name = countryName(code)
  return flag ? `${flag} ${name}` : name
}
