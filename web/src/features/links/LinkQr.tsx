import { DownloadIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { encodeQrMatrix } from '@/features/settings/TotpQr'
import { en } from '@/messages/en'

const quietZone = 4

export function qrPath(matrix: boolean[][]): string {
  return matrix
    .flatMap((row, y) => row.map((filled, x) => (filled ? `M${x + quietZone} ${y + quietZone}h1v1h-1z` : '')))
    .join('')
}

export function qrSvg(matrix: boolean[][]): string {
  const total = matrix.length + quietZone * 2
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${total} ${total}" width="${total * 8}" height="${total * 8}"><rect width="${total}" height="${total}" fill="#fff"/><path fill="#000" d="${qrPath(matrix)}"/></svg>`
}

/** Renders a QR code for a short URL entirely in the browser. */
export default function LinkQr({ url, name }: { url: string; name: string }) {
  let matrix: boolean[][] | null = null
  try {
    matrix = encodeQrMatrix(url)
  } catch {
    matrix = null
  }
  if (!matrix) return <p className="text-sm text-muted-foreground" role="status">{en.linksUi.qrTooLong}</p>
  const total = matrix.length + quietZone * 2
  const svg = qrSvg(matrix)
  const download = () => {
    const blob = new Blob([svg], { type: 'image/svg+xml' })
    const href = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = href
    anchor.download = `${name.replace(/[^a-z0-9_-]+/gi, '-').toLowerCase() || 'link'}-qr.svg`
    anchor.click()
    URL.revokeObjectURL(href)
  }
  return (
    <div className="flex flex-col items-center gap-3">
      <svg
        aria-label={en.linksUi.qrAlt.replace('{url}', url)}
        className="size-56 rounded-md border bg-white"
        role="img"
        viewBox={`0 0 ${total} ${total}`}
        xmlns="http://www.w3.org/2000/svg"
      >
        <rect width={total} height={total} fill="#fff" />
        <path d={qrPath(matrix)} fill="#000" />
      </svg>
      <p className="text-center text-xs text-muted-foreground">{en.linksUi.qrNote}</p>
      <Button className="min-h-11" type="button" variant="outline" onClick={download}>
        <DownloadIcon data-icon="inline-start" />
        {en.linksUi.qrDownload}
      </Button>
    </div>
  )
}
