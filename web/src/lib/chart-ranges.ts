import { currentBucketKey, type ChartInterval } from './chart'

/**
 * How many buckets at the start of the series precede the first event. There is no first-event date in the
 * API, so it is derived from the series: leading buckets with no activity, when real activity follows.
 * A single quiet bucket is ordinary zero traffic and is not marked; an all-zero series has no first event to mark.
 */
export function noDataBucketCount(values: Array<number | null>): number {
  const first = values.findIndex((value) => value !== null && value > 0)
  return first >= 2 ? first : 0
}

/**
 * True when the last drawn bucket is still filling: the range reaches today and, for hours, the last bucket is the
 * current hour. Future buckets are already trimmed, so for days, weeks and months the last one holds today.
 */
export function lastBucketIsPartial(
  lastBucket: string | undefined,
  rangeTo: string,
  interval: ChartInterval,
  now: Date,
  timezone: string,
): boolean {
  if (!lastBucket) return false
  const today = currentBucketKey('day', now, timezone)
  if (rangeTo < today) return false
  if (interval === 'hour') return lastBucket === currentBucketKey('hour', now, timezone)
  if (interval === 'day') return lastBucket.slice(0, 10) === today
  return lastBucket.slice(0, 10) <= today
}

export type SeriesPoint = { time: string; value: number | null }
export type SplitPoint = SeriesPoint & {
  /** Drawn as the solid line: closed buckets that follow the first event. */
  solid: number | null
  /** Drawn dashed: the last closed bucket (to join the lines) and the partial bucket. */
  partial: number | null
  partialBucket: boolean
  noData: boolean
}

/** Splits a series into the solid line, the dashed in-progress segment and the no-data lead-in. */
export function splitSeries<T extends SeriesPoint>(points: T[], partialLast: boolean): Array<T & SplitPoint> {
  const lead = noDataBucketCount(points.map((point) => point.value))
  const lastIndex = points.length - 1
  return points.map((point, index) => {
    const noData = index < lead
    const partialBucket = partialLast && index === lastIndex
    const joinsPartial = partialLast && index === lastIndex - 1
    return {
      ...point,
      noData,
      partialBucket,
      solid: noData || partialBucket ? null : point.value,
      partial: partialBucket || (joinsPartial && !noData) ? point.value : null,
    }
  })
}
