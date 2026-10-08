const ROOT = '/icons'

const browsers: Record<string, string> = {
  'android browser': 'android',
  'android webview': 'android-webview',
  android: 'android',
  aol: 'aol',
  beaker: 'beaker',
  blackberry: 'blackberry',
  brave: 'brave',
  bytespider: 'searchbot',
  chrome: 'chrome',
  'chrome mobile': 'chrome',
  'chrome mobile ios': 'crios',
  'chrome webview': 'chromium-webview',
  chromium: 'chrome',
  'chromium webview': 'chromium-webview',
  crios: 'crios',
  curl: 'curl',
  edge: 'edge-chromium',
  'edge chromium': 'edge-chromium',
  'edge ios': 'edge-ios',
  facebook: 'facebook',
  'facebook app': 'facebook',
  'facebook messenger': 'facebook',
  facebookexternalhit: 'facebook',
  firefox: 'firefox',
  'firefox ios': 'fxios',
  fxios: 'fxios',
  'google ads bot': 'searchbot',
  googlebot: 'searchbot',
  'headless chrome': 'searchbot',
  'huawei browser': 'android',
  ie: 'ie',
  instagram: 'instagram',
  'instagram app': 'instagram',
  'internet explorer': 'ie',
  'ios webview': 'ios-webview',
  ios: 'ios',
  kakaotalk: 'kakaotalk',
  'miui browser': 'miui',
  'microsoft edge': 'edge-chromium',
  miui: 'miui',
  'mobile safari': 'safari',
  opera: 'opera',
  'opera mini': 'opera-mini',
  'opera touch': 'opera',
  safari: 'safari',
  'samsung browser': 'samsung',
  'samsung internet': 'samsung',
  silk: 'silk',
  bingbot: 'searchbot',
  applebot: 'searchbot',
  twitterbot: 'searchbot',
  'yahoo ad monitoring': 'searchbot',
  yandex: 'yandexbrowser',
  'yandex browser': 'yandexbrowser',
  yandexbot: 'searchbot',
  yandexadnet: 'searchbot',
}

function iconPath(kind: 'browser' | 'os' | 'device', file: string): string {
  return `${ROOT}/${kind}/${file}.png`
}

function normalized(value: string): string {
  return value.trim().toLowerCase()
}

export function browserIcon(name: string): string {
  return iconPath('browser', browsers[normalized(name)] ?? 'unknown')
}

export function osIcon(name: string): string {
  const value = normalized(name)
  let file = 'unknown'

  if (/^windows(?: 10\/11| 10)?$/.test(value)) file = 'windows-10'
  else if (value === 'windows 11') file = 'windows-11'
  else if (value === 'windows 8.1') file = 'windows-8-1'
  else if (value === 'windows 8') file = 'windows-8'
  else if (value === 'windows 7') file = 'windows-7'
  else if (value === 'windows vista') file = 'windows-vista'
  else if (value === 'windows xp') file = 'windows-xp'
  else if (value === 'windows 2000') file = 'windows-2000'
  else if (value === 'windows 98') file = 'windows-98'
  else if (value === 'windows 95') file = 'windows-95'
  else if (value === 'windows me') file = 'windows-me'
  else if (value === 'windows server 2003') file = 'windows-server-2003'
  else if (value === 'windows phone' || value === 'windows mobile') file = 'windows-mobile'
  else if (/^(mac os|macos)(?:\s|$)/.test(value)) file = 'mac-os'
  else if (/^(ios|ipados)(?:\s|$)/.test(value)) file = 'ios'
  else if (/^android(?:\s|$)/.test(value)) file = 'android-os'
  else if (value === 'linux' || value === 'ubuntu') file = 'linux'
  else if (value === 'chromeos' || value === 'chrome os' || value === 'cros') file = 'chrome-os'
  else if (value === 'freebsd' || value === 'openbsd' || value === 'open bsd') file = 'open-bsd'
  else if (value === 'blackberry' || value === 'blackberry os') file = 'blackberry-os'

  return iconPath('os', file)
}

export function deviceIcon(name: string): string {
  const value = normalized(name)
  return iconPath('device', ['desktop', 'laptop', 'mobile', 'tablet'].includes(value) ? value : 'unknown')
}
