import { describe, expect, it } from 'vitest'

import { countryFlagSrc, countryLabel, countryName, normalizeCountryCode } from './country'

describe('country', () => {
  it('normalizes valid ISO codes and builds lowercase asset paths', () => {
    expect(normalizeCountryCode('IN')).toBe('IN')
    expect(normalizeCountryCode('us')).toBe('US')
    expect(countryFlagSrc('IN')).toBe('/flags/in.svg')
    expect(countryFlagSrc('us')).toBe('/flags/us.svg')
  })

  it('names countries in English', () => {
    expect(countryName('IN')).toBe('India')
    expect(countryName('DE')).toBe('Germany')
  })

  it('leaves empty and invalid codes alone', () => {
    expect(normalizeCountryCode('')).toBeUndefined()
    expect(normalizeCountryCode('1A')).toBeUndefined()
    expect(countryFlagSrc('')).toBeUndefined()
    expect(countryName('')).toBe('Unknown')
    expect(countryName('ZZZ')).toBe('ZZZ')
    expect(countryLabel('')).toBe('Unknown')
  })

  it('uses the country name as its text label', () => {
    expect(countryLabel('IN')).toBe('India')
    expect(countryLabel('XX')).not.toBe('XX')
  })
})
