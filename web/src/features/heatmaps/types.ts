import type { components } from '@/lib/api.gen'

export type HeatmapDevice = components['schemas']['HeatmapDevice']
export type HeatmapCell = components['schemas']['HeatmapCell']
export type ScrollBand = components['schemas']['ScrollBand']
export type HeatmapResponse = components['schemas']['HeatmapResponse']

export type HeatmapSite = {
  allowed_hosts: string[]
  heatmaps_enabled: boolean
}
