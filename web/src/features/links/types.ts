export type LinkStatus = 'active' | 'disabled' | 'expired' | 'limit_reached'

export type ShortLink = {
  id: number
  site_id: number
  slug: string
  name: string
  destination: string
  utm_source: string
  utm_medium: string
  utm_campaign: string
  utm_term: string
  utm_content: string
  enabled: boolean
  expires_at: string | null
  click_limit: number | null
  clicks: number
  status: LinkStatus
  short_path: string
  short_url: string
  created_at: string
  updated_at: string
}

export type LinkInput = {
  slug?: string
  name: string
  destination: string
  utm_source: string
  utm_medium: string
  utm_campaign: string
  utm_term: string
  utm_content: string
  enabled: boolean
  expires_at: string | null
  click_limit: number | null
}

export type LinkCount = { value: string; clicks: number }
export type DayCount = { day: string; count: number }

export type LinkStats = {
  from: string
  to: string
  total: number
  daily: DayCount[]
  countries: LinkCount[]
  referrers: LinkCount[]
  devices: LinkCount[]
}

export type PixelSnippet = { campaign: string; path: string; url: string; html: string }

export type TrackingPixel = {
  id: number
  site_id: number
  name: string
  tag: string
  gif_url: string
  svg_url: string
  html: string
  total: number
  views_30d: number
  created_at: string
}

export type TrackingPixelInput = { name: string; tag?: string }

export type PixelStats = {
  summary: { today: number; last_7: number; last_30: number; total: number }
  from: string
  to: string
  total: number
  daily: DayCount[]
  campaigns: { campaign: string; impressions: number }[]
}
