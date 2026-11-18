import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { fmt } from '@/lib/format'

import { useAppDebugFiles, useDeleteDebugFile } from './api'

const shortId = (id: string) => `${id.slice(0, 8)}…`

function size(bytes: number): string {
  if (bytes >= 1 << 20) return `${(bytes / (1 << 20)).toFixed(1)} MB`
  if (bytes >= 1 << 10) return `${Math.round(bytes / (1 << 10))} KB`
  return `${bytes} B`
}

/** The Symbols settings: uploaded debug files and the images events still miss. role gates deleting. */
export function DebugFiles({ siteId, role }: { siteId: number; role?: string }) {
  const files = useAppDebugFiles(siteId)
  const remove = useDeleteDebugFile(siteId)
  const canEdit = role === 'owner' || role === 'admin' || role === 'editor'
  const canEditOrgWide = role === 'owner' || role === 'admin'
  if (files.isPending) return <p role="status" className="text-sm text-muted-foreground">Loading debug files…</p>
  if (files.isError) return <p role="alert" className="text-sm text-destructive">{files.error.message}</p>
  const { files: uploaded, missing } = files.data
  return (
    <div className="flex min-w-0 flex-col gap-6">
      <Card>
        <CardHeader>
          <CardTitle>Missing symbols</CardTitle>
          <CardDescription>Debug files and ProGuard mappings that recent events needed and no upload covered. A row disappears when the file is uploaded.</CardDescription>
        </CardHeader>
        <CardContent>
          {missing.length === 0 ? <p className="text-sm text-muted-foreground">Nothing is missing.</p> : (
            <Table aria-label="Missing symbols">
              <TableHeader><TableRow><TableHead>Kind</TableHead><TableHead>File</TableHead><TableHead>Debug ID</TableHead><TableHead>Arch</TableHead><TableHead className="text-right">Events</TableHead><TableHead>Last seen</TableHead></TableRow></TableHeader>
              <TableBody>
                {missing.map((m) => (
                  <TableRow key={`${m.kind}-${m.debug_id}`}>
                    <TableCell>{m.kind === 'proguard' ? 'ProGuard mapping' : m.kind}</TableCell>
                    <TableCell>{m.code_file || '—'}</TableCell>
                    <TableCell><code className="font-mono text-xs" title={m.debug_id}>{shortId(m.debug_id)}</code></TableCell>
                    <TableCell>{m.arch || '—'}</TableCell>
                    <TableCell className="text-right tabular-nums">{fmt.number(m.events)}</TableCell>
                    <TableCell>{m.last_seen.slice(0, 10)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Debug files</CardTitle>
          <CardDescription>Uploaded with <code>sentry-cli debug-files upload</code>, <code>sentry-cli upload-proguard</code> or the Gradle plugin and Xcode build phase.</CardDescription>
        </CardHeader>
        <CardContent>
          {remove.isError ? <p role="alert" className="mb-2 text-sm text-destructive">{remove.error.message}</p> : null}
          {uploaded.length === 0 ? <p className="text-sm text-muted-foreground">No debug files uploaded yet.</p> : (
            <Table aria-label="Debug files">
              <TableHeader><TableRow><TableHead>Kind</TableHead><TableHead>Name</TableHead><TableHead>Debug ID</TableHead><TableHead>Arch</TableHead><TableHead>State</TableHead><TableHead className="text-right">Size</TableHead><TableHead><span className="sr-only">Actions</span></TableHead></TableRow></TableHeader>
              <TableBody>
                {uploaded.map((f) => (
                  <TableRow key={f.id}>
                    <TableCell>{f.kind}{f.org_wide ? <Badge className="ml-2" variant="outline">org-wide</Badge> : null}</TableCell>
                    <TableCell className="max-w-[16rem] truncate" title={f.object_name}>{f.object_name || '—'}</TableCell>
                    <TableCell><code className="font-mono text-xs" title={f.debug_id}>{shortId(f.debug_id)}</code></TableCell>
                    <TableCell>{f.arch || '—'}</TableCell>
                    <TableCell><Badge variant={f.cache_state === 'error' ? 'destructive' : 'secondary'}>{f.cache_state}</Badge></TableCell>
                    <TableCell className="text-right tabular-nums">{size(f.size)}</TableCell>
                    <TableCell className="text-right">
                      {canEdit && (!f.org_wide || canEditOrgWide) ? (
                        <AlertDialog>
                          <AlertDialogTrigger asChild><Button type="button" variant="outline" size="sm" aria-label={`Delete ${f.object_name || shortId(f.debug_id)}`}>Delete</Button></AlertDialogTrigger>
                          <AlertDialogContent>
                            <AlertDialogHeader>
                              <AlertDialogTitle>Delete this debug file?</AlertDialogTitle>
                              <AlertDialogDescription>
                                {f.org_wide ? 'It is shared by every project of the organization. ' : ''}New events stop resolving through it; stored events are not changed.
                              </AlertDialogDescription>
                            </AlertDialogHeader>
                            <AlertDialogFooter>
                              <AlertDialogCancel>Cancel</AlertDialogCancel>
                              <AlertDialogAction onClick={() => remove.mutate(f.id)}>Delete file</AlertDialogAction>
                            </AlertDialogFooter>
                          </AlertDialogContent>
                        </AlertDialog>
                      ) : null}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
