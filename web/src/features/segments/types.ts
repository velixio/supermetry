import type { Filter } from '@/lib/search'

export type Segment = {
  id: number
  site_id: number
  name: string
  filters: Filter[]
  created_by?: number | null
  created_at?: string
  updated_at?: string
}

export type SegmentInput = {
  name: string
  filters: Filter[]
}
