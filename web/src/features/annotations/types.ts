export type Annotation = {
  id: number
  site_id: number
  day: string
  text: string
  created_by?: number | null
  created_at: string
}

export type AnnotationInput = Pick<Annotation, 'day' | 'text'>
