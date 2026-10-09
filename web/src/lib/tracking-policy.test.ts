import { describe, expect, it } from 'vitest'
import { isAnonymousDimensionAllowed, isAnonymousFilterAllowed, isAnonymousTracking, trackingPolicyKnown } from './tracking-policy'

describe('tracking policy capabilities', () => {
  it('fails closed for missing policy, and uses instance rather than project profile permission', () => {
    expect(trackingPolicyKnown({})).toBe(false)
    expect(isAnonymousTracking({})).toBe(true)
    const site = { tracking_policy: { instance_mode: 'identified' as const, effective_mode: 'anonymous' as const, configured_profiles_enabled: false, restrictions: ['project_profiles_disabled'] } }
    expect(trackingPolicyKnown(site)).toBe(true)
    expect(isAnonymousTracking(site)).toBe(false)
    expect(isAnonymousTracking({ tracking_policy: { ...site.tracking_policy, instance_mode: 'anonymous' } })).toBe(true)
  })
  it('permits canonical exact filters only, never free text or unknown buckets', () => {
    for (const [dimension, value] of [['country','US'],['device','mobile'],['browser','Chrome'],['os','Linux'],['channel','Direct']]) {
      expect(isAnonymousFilterAllowed({ dimension, operator: 'is', values: [value] })).toBe(true)
      expect(isAnonymousFilterAllowed({ dimension, operator: 'contains', values: [value] })).toBe(false)
      expect(isAnonymousFilterAllowed({ dimension, operator: 'is', values: ['private-user-value'] })).toBe(false)
      expect(isAnonymousFilterAllowed({ dimension, operator: 'is', values: [''] })).toBe(false)
    }
    for (const dimension of ['page','source','utm_source','goal','event','prop:email','browser_version']) {
      expect(isAnonymousDimensionAllowed(dimension)).toBe(false)
      expect(isAnonymousFilterAllowed({ dimension, operator: 'is', values: ['anything'] })).toBe(false)
    }
    expect(isAnonymousFilterAllowed({dimension:'country',operator:'is',values:['ZZ']})).toBe(false)
    expect(isAnonymousFilterAllowed({dimension:'country',operator:'is',values:['XK', 'AC', 'TA']})).toBe(true)
  })
})
