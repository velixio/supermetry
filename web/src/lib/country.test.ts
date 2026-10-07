import { describe, expect, it } from 'vitest'

import { countryFlag, countryLabel, countryName } from './country'

describe('country', () => {
  it('turns an ISO code into a flag emoji', () => {
    expect(countryFlag('IN')).toBe('🇮🇳')
    expect(countryFlag('us')).toBe('🇺🇸')
  })

  it('names countries in English', () => {
    expect(countryName('IN')).toBe('India')
    expect(countryName('DE')).toBe('Germany')
  })

  it('leaves empty and invalid codes alone', () => {
    expect(countryFlag('')).toBe('')
    expect(countryFlag('1A')).toBe('')
    expect(countryName('')).toBe('Unknown')
    expect(countryName('ZZZ')).toBe('ZZZ')
    expect(countryLabel('')).toBe('Unknown')
  })

  it('combines flag and name', () => {
    expect(countryLabel('IN')).toBe('🇮🇳 India')
  })
})
