// Finds UI strings in Title Case ("Views Per Visit") so labels stay sentence case.
export const allowedWords = new Set([
  'GitHub', 'GitLab', 'Google', 'Search', 'Console', 'UTM', 'API', 'MCP', 'CSV', 'JSON', 'GA4', 'GA', 'URL', 'URLs', 'SQL', 'HTTP', 'HTTPS',
  'IP', 'DNS', 'SSO', 'SAML', 'OAuth', 'TOTP', 'PDF', 'SDK', 'CLI', 'UI', 'ID', 'IDs', 'iOS', 'macOS', 'Android', 'Windows', 'Linux', 'Chrome',
  'Firefox', 'Safari', 'Edge', 'Supermetry', 'Mixpanel', 'Amplitude', 'Plausible', 'Umami', 'Slack', 'Discord', 'Telegram', 'Webhook', 'Webhooks',
  'Stripe', 'Paddle', 'Postgres', 'PostgreSQL', 'Redis', 'Docker', 'WordPress', 'Shopify', 'Next.js', 'React', 'Vue', 'Svelte', 'Astro', 'Hugo',
  'Cloudflare', 'Vercel', 'Netlify', 'OpenAPI', 'Claude', 'ChatGPT', 'Bing', 'DuckDuckGo', 'Baidu', 'Yandex', 'Twitter', 'Facebook', 'LinkedIn',
  'Reddit', 'YouTube', 'Hacker', 'News', 'Mastodon', 'Bluesky', 'GeoIP', 'MaxMind', 'DB-IP', 'IPv4', 'IPv6', 'LCP', 'CLS', 'INP', 'FCP', 'TTFB',
  'KPI', 'UTC', 'GDPR', 'CCPA', 'PECR', 'SOC', 'RSS', 'XML', 'HTML', 'CSS', 'JS', 'TypeScript', 'JavaScript', 'Go', 'Python', 'Ruby', 'PHP', 'Node.js',
  'English', 'German', 'Spanish', 'French', 'Bengali', 'Hindi', 'Portuguese', 'Italian', 'Dutch', 'Japanese', 'Chinese', 'Korean', 'Russian',
  'January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December',
  'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun',
  'United', 'States', 'Kingdom', 'Europe', 'Asia', 'Africa', 'America', 'Oceania', 'Antarctica', 'India', 'Germany', 'France', 'China', 'Japan', 'Brazil',
  'Vitals', 'Markdown', 'Analytics', 'Checkout', 'Organization', 'MIT', 'Apache', 'AGPL', 'Pro', 'Cloud', 'Core', 'Enterprise', 'Free',
])

/**
 * Checks labels only: short strings (up to 7 words) with no sentence punctuation, such as headings,
 * buttons, column titles and tab names. Prose sentences are skipped because they carry product
 * names (Stripe Dashboard, Web Vitals) that a word list cannot keep up with.
 * A label is Title Case when any word after the first starts with a capital letter and is not an
 * allow-listed name or acronym (so "Bounce Rate" and "Views Per Visit" fail, "Connect GitHub" passes).
 */
export function titleCaseWords(text: string): string[] {
  // Drop shortcut prefixes ("D — Toggle") and leading symbols ("+ Add", "· Configured").
  const clean = text.replace(/\{[^}]*\}/g, ' ').replace(/^.*?\s—\s/, '').replace(/^[^\p{L}\p{N}]+/u, '').trim()
  const words = clean.split(/\s+/).filter(Boolean)
  if (words.length < 2 || words.length > 7 || /[.!?]/.test(clean.replace(/\.\.\.|…/g, ''))) return []
  const bad: string[] = []
  for (const raw of words.slice(1)) {
    const word = raw.replace(/^[("'“]+|[)"'”,;:…]+$/g, '')
    if (!/^[A-Z][a-z]/.test(word)) continue // acronyms, digits and lower case are fine
    if (allowedWords.has(word)) continue
    bad.push(word)
  }
  return bad
}

export function isTitleCase(text: string): boolean {
  return titleCaseWords(text).length >= 1
}
