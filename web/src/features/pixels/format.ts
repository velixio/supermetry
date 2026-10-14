export function formatViews(n: number): string {
  const units: [number, string][] = [[1e9, 'B'], [1e6, 'M'], [1e3, 'k']]
  for (let i = 0; i < units.length; i++) {
    const [div, suffix] = units[i]
    if (n < div) continue
    const tenths = Math.floor((n * 10 + div / 2) / div)
    if (tenths >= 10000 && i > 0) return `1${units[i - 1][1]}`
    return tenths % 10 === 0 ? `${tenths / 10}${suffix}` : `${Math.floor(tenths / 10)}.${tenths % 10}${suffix}`
  }
  return String(n)
}
