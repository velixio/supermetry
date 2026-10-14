import { fmt } from '@/lib/format'
import { en } from '@/messages/en'

import type { JourneyLink, JourneyNode } from './types'

type PositionedNode = JourneyNode & { x: number; y: number; height: number }

const width = 960
const t = en.journeyUi
const minNode = 40
const gap = 12
const pad = 24
const nodeWidth = 150

function nodeKey(step: number, path: string): string {
  return `${step}:${path}`
}

function displayPath(path: string): string {
  if (path === 'exit') return t.exit
  if (path === 'other') return t.other
  return path
}

function nodeHeights(nodes: JourneyNode[], maximum: number): number[] {
  return nodes.map((node) => Math.max(minNode, (node.visitors / maximum) * 92))
}

function flowHeight(nodes: JourneyNode[]): number {
  const maximum = Math.max(1, ...nodes.map((node) => node.visitors))
  const steps = [...new Set(nodes.map((node) => node.step))]
  const tallest = Math.max(
    0,
    ...steps.map((step) => {
      const atStep = nodes.filter((node) => node.step === step)
      return nodeHeights(atStep, maximum).reduce((sum, value) => sum + value, 0) + gap * Math.max(0, atStep.length - 1)
    }),
  )
  return Math.max(200, tallest + pad * 2)
}

function positionedNodes(nodes: JourneyNode[]): PositionedNode[] {
  const steps = [...new Set(nodes.map((node) => node.step))].sort((a, b) => a - b)
  const maximum = Math.max(1, ...nodes.map((node) => node.visitors))
  return steps.flatMap((step, stepIndex) => {
    const atStep = nodes.filter((node) => node.step === step)
    const heights = nodeHeights(atStep, maximum)
    let y = pad
    return atStep.map((node, nodeIndex) => {
      const positioned = {
        ...node,
        x: steps.length === 1 ? (width - nodeWidth) / 2 : 24 + stepIndex * ((width - nodeWidth - 48) / (steps.length - 1)),
        y,
        height: heights[nodeIndex],
      }
      y += heights[nodeIndex] + gap
      return positioned
    })
  })
}

export function JourneyFlow({ nodes, links }: { nodes: JourneyNode[]; links: JourneyLink[] }) {
  const positioned = positionedNodes(nodes)
  const byKey = new Map(positioned.map((node) => [nodeKey(node.step, node.path), node]))
  const height = flowHeight(nodes)
  const maximumLink = Math.max(1, ...links.map((link) => link.visitors))

  return (
    <section aria-label={t.results} className="min-w-0 overflow-x-auto rounded-xl border bg-card p-4 focus-visible:ring-2 focus-visible:ring-ring" tabIndex={0}>
      <svg
        aria-label={t.diagram}
        className="min-w-[60rem]"
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
            <title>{displayPath(node.path)}: {t.visitors(fmt.number(node.visitors))}</title>
            <rect
              className={node.path.toLowerCase() === 'exit' ? 'fill-muted stroke-border' : 'fill-primary/15 stroke-primary/40'}
              height={node.height}
              rx="8"
              width={nodeWidth}
              x={node.x}
              y={node.y}
            />
            <text className="fill-foreground text-[12px] font-medium" x={node.x + 10} y={node.y + 17}>
              {displayPath(node.path).length > 18 ? `${displayPath(node.path).slice(0, 17)}…` : displayPath(node.path)}
            </text>
            <text className="fill-muted-foreground text-[11px]" x={node.x + 10} y={node.y + 32}>
              {t.visitors(fmt.number(node.visitors))}
            </text>
          </g>
        ))}
      </svg>
      <div className="sr-only"><table aria-label={t.links}>
        <thead><tr><th>{t.path}</th><th>{t.visitorsHeading}</th></tr></thead>
        <tbody>
          {links.map((link, index) => (
            <tr key={`${link.from_step}:table:${link.from_path}:${link.to_path}:${index}`}>
              <th>{displayPath(link.from_path)} → {displayPath(link.to_path)}</th>
              <td>{link.visitors}</td>
            </tr>
          ))}
        </tbody>
      </table></div>
      <p className="mt-2 text-xs text-muted-foreground lg:hidden">{t.scrollHint}</p>
    </section>
  )
}
