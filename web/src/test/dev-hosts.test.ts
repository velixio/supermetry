import { afterEach, describe, expect, it, vi } from 'vitest'

import { devAllowedHosts } from '../../dev-hosts'

afterEach(() => vi.restoreAllMocks())

describe('devAllowedHosts', () => {
  it('allows only Vite defaults when nothing is set', () => {
    expect(devAllowedHosts({})).toEqual([])
  })

  it('allows the hostname of SUPERMETRY_BASE_URL', () => {
    expect(devAllowedHosts({ SUPERMETRY_BASE_URL: 'https://ana.voicestudio.sh' })).toEqual(['ana.voicestudio.sh'])
    expect(devAllowedHosts({ SUPERMETRY_BASE_URL: 'https://Ana.VoiceStudio.sh:8443/some/path?x=1' })).toEqual(['ana.voicestudio.sh'])
  })

  it('adds nothing for loopback base URLs', () => {
    for (const url of ['http://localhost:8787', 'http://127.0.0.1:8787', 'http://[::1]:8787', 'http://LOCALHOST']) {
      expect(devAllowedHosts({ SUPERMETRY_BASE_URL: url }), url).toEqual([])
    }
  })

  it('handles IPv6 literals by stripping the brackets', () => {
    expect(devAllowedHosts({ SUPERMETRY_BASE_URL: 'http://[2001:db8::1]:8787' })).toEqual(['2001:db8::1'])
  })

  it('ignores unparseable base URLs', () => {
    for (const url of ['', 'not a url', 'ana.voicestudio.sh', 'http://', '//x']) {
      expect(devAllowedHosts({ SUPERMETRY_BASE_URL: url }), url).toEqual([])
    }
  })

  it('parses SUPERMETRY_DEV_ALLOWED_HOSTS: trims, drops empties, dedupes, keeps wildcards', () => {
    expect(devAllowedHosts({ SUPERMETRY_DEV_ALLOWED_HOSTS: ' a.example.com , ,.example.org,a.example.com,' })).toEqual([
      'a.example.com',
      '.example.org',
    ])
  })

  it('refuses values that would disable the host check and warns once', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(devAllowedHosts({ SUPERMETRY_DEV_ALLOWED_HOSTS: 'true' })).toEqual([])
    expect(devAllowedHosts({ SUPERMETRY_DEV_ALLOWED_HOSTS: '*, a.example.com ,TRUE' })).toEqual(['a.example.com'])
    expect(warn).toHaveBeenCalledTimes(2)
    expect(String(warn.mock.calls[0][0])).toContain('SUPERMETRY_DEV_ALLOWED_HOSTS')
  })

  it('merges both sources without duplicates', () => {
    expect(
      devAllowedHosts({
        SUPERMETRY_BASE_URL: 'https://ana.voicestudio.sh',
        SUPERMETRY_DEV_ALLOWED_HOSTS: 'ana.voicestudio.sh,.example.org',
      }),
    ).toEqual(['ana.voicestudio.sh', '.example.org'])
  })
})
