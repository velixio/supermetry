import { ChevronDownIcon, Trash2Icon } from 'lucide-react'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

import { useDeletePixel, useUpdatePixel, type Pixel } from './api'
import { formatViews } from './format'
import { pixelSnippets } from './snippets'

function copy(text: string) {
  return navigator.clipboard.writeText(text)
}

export function PixelList({ siteId, pixels, homepage }: { siteId: number; pixels: Pixel[]; homepage: string }) {
  const update = useUpdatePixel(siteId)
  const remove = useDeletePixel(siteId)
  return (
    <section aria-labelledby="pixel-list-title" className="rounded-xl border bg-card">
      <div className="border-b p-4"><h2 className="font-medium" id="pixel-list-title">Pixels</h2></div>
      {pixels.length === 0 ? <p className="p-6 text-sm text-muted-foreground">Create a pixel to start counting views.</p> : (
        <Table>
          <TableHeader><TableRow><TableHead>Name</TableHead><TableHead>Total</TableHead><TableHead>30 days</TableHead><TableHead>Enabled</TableHead><TableHead>Badge</TableHead><TableHead><span className="sr-only">Actions</span></TableHead></TableRow></TableHeader>
          <TableBody>
            {pixels.map((pixel) => {
              const snippets = pixelSnippets(pixel, homepage)
              return (
                <TableRow key={pixel.id}>
                  <TableCell className="font-medium">{pixel.name}</TableCell>
                  <TableCell className="tabular-nums">{formatViews(pixel.total)}</TableCell>
                  <TableCell className="tabular-nums">{formatViews(pixel.views_30d)}</TableCell>
                  <TableCell><Switch aria-label={`${pixel.name} enabled`} checked={pixel.enabled} disabled={update.isPending} onCheckedChange={(enabled) => update.mutate({ id: pixel.id, enabled })} /></TableCell>
                  <TableCell><img src={pixel.svg_url} alt={`${pixel.name} badge preview`} height={20} /></TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-1">
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild><Button variant="outline" size="sm" aria-label="Copy linked Markdown badge and more">Copy <ChevronDownIcon /></Button></DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem onSelect={() => void copy(snippets.linkedMarkdown)}>Copy linked Markdown badge</DropdownMenuItem>
                          <DropdownMenuItem onSelect={() => void copy(snippets.markdown)}>Copy Markdown badge</DropdownMenuItem>
                          <DropdownMenuItem onSelect={() => void copy(snippets.invisibleMarkdown)}>Copy invisible Markdown pixel</DropdownMenuItem>
                          <DropdownMenuItem onSelect={() => void copy(snippets.html)}>Copy HTML &lt;img&gt;</DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                      <AlertDialog>
                        <AlertDialogTrigger asChild><Button variant="ghost" size="icon-sm" aria-label={`Delete ${pixel.name}`}><Trash2Icon /></Button></AlertDialogTrigger>
                        <AlertDialogContent>
                          <AlertDialogHeader><AlertDialogTitle>Delete {pixel.name}?</AlertDialogTitle><AlertDialogDescription>Its badge will keep rendering, but it will no longer count views.</AlertDialogDescription></AlertDialogHeader>
                          <AlertDialogFooter><AlertDialogCancel>Cancel</AlertDialogCancel><AlertDialogAction variant="destructive" onClick={() => remove.mutate(pixel.id)}>Delete pixel</AlertDialogAction></AlertDialogFooter>
                        </AlertDialogContent>
                      </AlertDialog>
                    </div>
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      )}
    </section>
  )
}
