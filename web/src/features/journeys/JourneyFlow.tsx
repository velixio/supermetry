import { fmt } from '@/lib/format'

import type { JourneyLink, JourneyNode } from './types'

type PositionedNode = JourneyNode & { x: number; y: number; height: number }

const width = 960
const height = 440
const nodeWidth = 150

function nodeKey(step: number, path: string): string {
  return `${step}:${path}`
}

function displayPath(path: string): string {
  if (path === 'exit') return 'Exit'
  if (path === 'other') return 'Other'
  return path
}

function positionedNodes(nodes: JourneyNode[]): PositionedNode[] {
  const steps = [...new Set(nodes.map((node) => node.step))].sort((a, b) => a - b)
  const maximum = Math.max(1, ...nodes.map((node) => node.visitors))
  return steps.flatMap((step, stepIndex) => {
    const atStep = nodes.filter((node) => node.step === step)
    const gap = 12
    const available = height - 52 - gap * Math.max(0, atStep.length - 1)
    const rawHeights = atStep.map((node) => Math.max(28, (node.visitors / maximum) * 92))
    const scale = rawHeights.reduce((sum, value) => sum + value, 0) > available
      ? available / rawHeights.reduce((sum, value) => sum + value, 0)
      : 1
    let y = 36
    return atStep.map((node, nodeIndex) => {
      const nodeHeight = rawHeights[nodeIndex] * scale
      const positioned = {
        ...node,
        x: steps.length === 1 ? (width - nodeWidth) / 2 : 24 + stepIndex * ((width - nodeWidth - 48) / (steps.length - 1)),
        y,
        height: nodeHeight,
      }
      y += nodeHeight + gap
      return positioned
    })
  })
}

export function JourneyFlow({ nodes, links }: { nodes: JourneyNode[]; links: JourneyLink[] }) {
  const positioned = positionedNodes(nodes)
  const byKey = new Map(positioned.map((node) => [nodeKey(node.step, node.path), node]))
  const maximumLink = Math.max(1, ...links.map((link) => link.visitors))

  return (
    <section aria-label="Journey results" className="min-w-0 overflow-x-auto rounded-xl border bg-card p-4">
      <svg
        aria-label="User journey path flow"
        className="min-w-[44rem]"
        role="img"
        viewBox={`0 0 ${width} ${height}`}
      >
        {links.map((link, index) => {
          const from = byKey.get(nodeKey(link.from_step, link.from_path))
          const to = byKey.get(nodeKey(link.from_step + 1, link.to_path))
          if (!from || !to) return null
          const fromX = from.x + nodeWidth
          const fromY = from.y + from.height / 2
          const toY = to.y + to.height / 2
          const curve = Math.max(40, (to.x - fromX) / 2)
          return (
            <path
              className="fill-none stroke-primary/25"
              d={`M ${fromX} ${fromY} C ${fromX + curve} ${fromY}, ${to.x - curve} ${toY}, ${to.x} ${toY}`}
              key={`${link.from_step}:${link.from_path}:${link.to_path}:${index}`}
              strokeWidth={Math.max(2, (link.visitors / maximumLink) * 18)}
            />
          )
        })}
        {positioned.map((node) => (
          <g key={nodeKey(node.step, node.path)}>
            <rect
              className={node.path.toLowerCase() === 'exit' ? 'fill-muted stroke-border' : 'fill-primary/15 stroke-primary/40'}
              height={node.height}
              rx="8"
              width={nodeWidth}
              x={node.x}
              y={node.y}
            />
            <text className="fill-foreground text-[12px] font-medium" x={node.x + 10} y={node.y + 18}>
              {displayPath(node.path).length > 18 ? `${displayPath(node.path).slice(0, 17)}…` : displayPath(node.path)}
            </text>
            <text className="fill-muted-foreground text-[11px]" x={node.x + 10} y={node.y + 34}>
              {fmt.number(node.visitors)} visitors
            </text>
          </g>
        ))}
      </svg>
      <table aria-label="Journey links" className="sr-only">
        <thead><tr><th>Path</th><th>Visitors</th></tr></thead>
        <tbody>
          {links.map((link, index) => (
            <tr key={`${link.from_step}:table:${link.from_path}:${link.to_path}:${index}`}>
              <th>{displayPath(link.from_path)} → {displayPath(link.to_path)}</th>
              <td>{link.visitors}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  )
}
