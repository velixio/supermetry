import type { components } from '@/lib/api.gen'

export type JourneyNode = components['schemas']['JourneyNode']
export type JourneyLink = components['schemas']['JourneyLink']
export type JourneyResponse = components['schemas']['JourneyResponse']

export type JourneyDirection = 'start' | 'end'
