import type { TraceData, TransactionDetailData, TransactionRowData } from './api'

export const txRow = (over: Partial<TransactionRowData> = {}): TransactionRowData => ({
  name: 'GET /items', op: 'http.server', count: 110, errors: 5, throughput_per_min: 1.5, error_rate: 5 / 110,
  p50_ms: 55.04, p75_ms: 70, p90_ms: 90, p95_ms: 97.234, p99_ms: 200, ...over,
})

/** Three spans over a 100 ms trace: the segment at 0 ms, a DB child at 10 ms and a failing HTTP child at 40 ms. */
export const trace3 = (): TraceData => ({
  trace_id: 'a'.repeat(32), duration_ms: 100, truncated: false,
  spans: [
    { span_id: '1'.repeat(16), depth: 0, is_segment: true, name: 'GET /items', op: 'http.server', description: '', status: 'ok', start: '2026-11-18T10:00:00Z', start_offset_ms: 0, duration_ms: 100 },
    { span_id: '2'.repeat(16), parent_id: '1'.repeat(16), depth: 1, is_segment: false, name: '', op: 'db', description: 'select * from users where id = ?', status: 'ok', start: '2026-11-18T10:00:00.010Z', start_offset_ms: 10, duration_ms: 20 },
    { span_id: '3'.repeat(16), parent_id: '1'.repeat(16), depth: 1, is_segment: false, name: '', op: 'http.client', description: 'GET https://api.example.com/v1/items/*', status: 'internal_error', http_status: 502, start: '2026-11-18T10:00:00.040Z', start_offset_ms: 40, duration_ms: 50 },
  ],
  errors: [{ event_id: 'e'.repeat(32), issue_id: 7, title: 'TypeError: x is undefined', ts: '2026-11-18T10:00:00.050Z', span_id: '3'.repeat(16) }],
})

export const detailData = (): TransactionDetailData => ({
  name: 'GET /items',
  summary: txRow(),
  series: [{ date: '2026-11-17', count: 50, errors: 2, p50_ms: 50, p95_ms: 95 }, { date: '2026-11-18', count: 60, errors: 3, p50_ms: 56, p95_ms: 99 }],
  span_groups: [{ op: 'db', description: 'select * from users where id = ?', count: 100, errors: 0, error_rate: 0, total_ms: 1200.5, avg_ms: 12.005, p95_ms: 30 }],
  slowest: [{ trace_id: 'b'.repeat(32), start: '2026-11-18T10:00:00Z', duration_ms: 480.25, release: '1.2.0' }],
  app_start: [{ type: 'cold', count: 4, p50_ms: 1000, p95_ms: 3000 }],
})
