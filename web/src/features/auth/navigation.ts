export function safeAuthNext(next: string): string {
  if (!next.startsWith('/') || next.startsWith('//') || /[\\\r\n]/.test(next)) return '/'
  const parsed = new URL(next, window.location.origin)
  if (parsed.origin !== window.location.origin) return '/'
  return `${parsed.pathname}${parsed.search}${parsed.hash}`
}
