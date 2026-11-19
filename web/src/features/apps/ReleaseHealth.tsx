import { Bar, BarChart, CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { fmt } from '@/lib/format'

import type { AppOverviewData } from './api'
import { formatRate } from './format'

export const EVENTS_NOTE = 'from error events'

/** Session status counts: healthy is what is left after errored, abnormal and crashed. Abnormal (ANR, hang) is not a crash. */
export function statusBreakdown(data: Pick<AppOverviewData, 'sessions' | 'crash_free_sessions' | 'abnormal_sessions' | 'errored_sessions'>) {
  const crashed = data.crash_free_sessions === null ? 0 : Math.round((1 - data.crash_free_sessions) * data.sessions)
  const healthy = Math.max(0, data.sessions - data.errored_sessions - data.abnormal_sessions - crashed)
  return [
    { status: 'Healthy', count: healthy, fill: 'var(--chart-2)' },
    { status: 'Errored', count: data.errored_sessions, fill: 'var(--chart-4)' },
    { status: 'Abnormal', count: data.abnormal_sessions, fill: 'var(--chart-5)' },
    { status: 'Crashed', count: crashed, fill: 'var(--chart-1)' },
  ]
}

export function ReleaseHealth({ data }: { data: AppOverviewData }) {
  const fromEvents = data.health_source === 'events'
  const statuses = statusBreakdown(data)
  const errorTotal = data.handled_errors + data.unhandled_errors
  return (
    <div className="grid gap-4 lg:grid-cols-3" aria-label="Release health">
      <Card className="lg:col-span-2">
        <CardHeader>
          <CardTitle>Crash-free sessions</CardTitle>
          <CardDescription>Daily rate{fromEvents ? ` (${EVENTS_NOTE})` : ''}.</CardDescription>
        </CardHeader>
        <CardContent>
          {data.series.length === 0 ? <p className="rounded-lg bg-muted/30 p-8 text-center text-sm text-muted-foreground">No sessions in this period.</p> : (
            <div className="h-56" role="img" aria-label="Daily crash-free sessions">
              <ResponsiveContainer width="100%" height="100%">
                <LineChart accessibilityLayer data={data.series}>
                  <CartesianGrid vertical={false} strokeDasharray="3 3" />
                  <XAxis dataKey="day" tickLine={false} axisLine={false} minTickGap={24} />
                  <YAxis domain={[0, 1]} tickFormatter={(value: number) => `${Math.round(value * 100)}%`} tickLine={false} axisLine={false} width={44} />
                  <Tooltip formatter={(value) => formatRate(typeof value === 'number' ? value : null)} />
                  <Line dataKey="crash_free_sessions" name="Crash-free sessions" stroke="var(--chart-3)" dot={false} connectNulls />
                </LineChart>
              </ResponsiveContainer>
            </div>
          )}
        </CardContent>
      </Card>
      <div className="flex flex-col gap-4">
        <Card>
          <CardHeader><CardTitle>Session status</CardTitle><CardDescription>{fmt.number(data.sessions)} sessions.</CardDescription></CardHeader>
          <CardContent>
            <ul className="flex flex-col gap-1 text-sm" aria-label="Session status breakdown">
              {statuses.map((item) => (
                <li key={item.status} className="flex items-center justify-between gap-2">
                  <span className="flex items-center gap-2"><span aria-hidden className="size-2 rounded-full" style={{ background: item.fill }} />{item.status}</span>
                  <span className="tabular-nums">{fmt.number(item.count)}</span>
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle>Handled vs unhandled</CardTitle><CardDescription>{fmt.number(errorTotal)} errors.</CardDescription></CardHeader>
          <CardContent>
            <div className="h-20" role="img" aria-label={`Handled errors ${data.handled_errors}, unhandled errors ${data.unhandled_errors}`}>
              <ResponsiveContainer width="100%" height="100%">
                <BarChart accessibilityLayer layout="vertical" data={[{ name: 'Errors', handled: data.handled_errors, unhandled: data.unhandled_errors }]}>
                  <XAxis type="number" hide />
                  <YAxis type="category" dataKey="name" hide />
                  <Tooltip />
                  <Bar dataKey="handled" name="Handled" stackId="e" fill="var(--chart-2)" />
                  <Bar dataKey="unhandled" name="Unhandled" stackId="e" fill="var(--chart-1)" />
                </BarChart>
              </ResponsiveContainer>
            </div>
            <p className="mt-2 flex justify-between text-sm"><span>Handled <span className="tabular-nums">{fmt.number(data.handled_errors)}</span></span><span>Unhandled <span className="tabular-nums">{fmt.number(data.unhandled_errors)}</span></span></p>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
