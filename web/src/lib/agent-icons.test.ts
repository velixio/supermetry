import { describe, expect, it } from 'vitest'

import { browserIcon, deviceIcon, osIcon } from './agent-icons'

describe('agent icon mapping', () => {
  it.each([
    ['Chrome', 'chrome'], ['Mobile Safari', 'safari'], ['Safari', 'safari'], ['iOS', 'ios'],
    ['Edge', 'edge-chromium'], ['Microsoft Edge', 'edge-chromium'], ['Edge Chromium', 'edge-chromium'],
    ['Firefox', 'firefox'], ['Samsung Browser', 'samsung'], ['Samsung Internet', 'samsung'],
    ['Opera', 'opera'], ['Opera Mini', 'opera-mini'], ['Opera Touch', 'opera'], ['Brave', 'brave'],
    ['Yandex', 'yandexbrowser'], ['Yandex Browser', 'yandexbrowser'], ['Chrome WebView', 'chromium-webview'],
    ['Android browser', 'android'], ['Facebook App', 'facebook'], ['Facebook Messenger', 'facebook'], ['Instagram App', 'instagram'],
    ['Silk', 'silk'], ['Miui Browser', 'miui'], ['Internet Explorer', 'ie'], ['BlackBerry', 'blackberry'],
    ['Googlebot', 'searchbot'], ['Bytespider', 'searchbot'], ['Applebot', 'searchbot'],
    ['Bingbot', 'searchbot'], ['YandexBot', 'searchbot'], ['Google Ads Bot', 'searchbot'],
    ['Yahoo Ad monitoring', 'searchbot'], ['Headless Chrome', 'searchbot'], ['TikTok App', 'unknown'],
    ['Huawei Browser', 'android'], ['NetFront', 'unknown'], ['Vivaldi', 'unknown'],
  ])('maps browser %s', (name, file) => {
    expect(browserIcon(name)).toBe(`/icons/browser/${file}.png`)
    expect(browserIcon(name.toUpperCase())).toBe(`/icons/browser/${file}.png`)
  })

  it.each([
    ['Windows', 'windows-10'], ['Windows 10/11', 'windows-10'], ['Windows 11', 'windows-11'], ['Windows 8.1', 'windows-8-1'],
    ['Windows 7', 'windows-7'], ['Windows Phone', 'windows-mobile'], ['macOS', 'mac-os'],
    ['Mac OS', 'mac-os'], ['iOS', 'ios'], ['iPadOS', 'ios'], ['Android', 'android-os'],
    ['Linux', 'linux'], ['Ubuntu', 'linux'], ['ChromeOS', 'chrome-os'], ['Chrome OS', 'chrome-os'],
    ['Windows XP', 'windows-xp'], ['Windows Vista', 'windows-vista'], ['Windows 98', 'windows-98'],
    ['Windows ME', 'windows-me'], ['Windows 95', 'windows-95'], ['Windows Server 2003', 'windows-server-2003'],
    ['FreeBSD', 'open-bsd'], ['BlackBerry', 'blackberry-os'], ['Harmony', 'unknown'],
  ])('maps OS %s', (name, file) => {
    expect(osIcon(name)).toBe(`/icons/os/${file}.png`)
    expect(osIcon(name.toUpperCase())).toBe(`/icons/os/${file}.png`)
  })

  it.each(['desktop', 'laptop', 'mobile', 'tablet'])('maps device %s', (name) => {
    expect(deviceIcon(name)).toBe(`/icons/device/${name}.png`)
  })

  it('falls back for empty and unmapped values', () => {
    expect(browserIcon('')).toBe('/icons/browser/unknown.png')
    expect(browserIcon('Something New')).toBe('/icons/browser/unknown.png')
    expect(osIcon('')).toBe('/icons/os/unknown.png')
    expect(deviceIcon('watch')).toBe('/icons/device/unknown.png')
  })
})
