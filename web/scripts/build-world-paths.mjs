import { writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

// Natural Earth 1:110m admin-0 countries, public domain:
// https://raw.githubusercontent.com/nvkelso/natural-earth-vector/master/geojson/ne_110m_admin_0_countries.geojson
const SOURCE = 'https://raw.githubusercontent.com/nvkelso/natural-earth-vector/master/geojson/ne_110m_admin_0_countries.geojson'
const WIDTH = 1_000
const HEIGHT = 500
const TOLERANCE = 0.35

const response = await fetch(SOURCE)
if (!response.ok) throw new Error(`Natural Earth download failed: ${response.status}`)
const collection = await response.json()
const paths = {}

for (const feature of collection.features) {
  const code = feature.properties?.ISO_A2
  if (!/^[A-Z]{2}$/.test(code)) continue
  const polygons = feature.geometry.type === 'Polygon'
    ? [feature.geometry.coordinates]
    : feature.geometry.type === 'MultiPolygon'
      ? feature.geometry.coordinates
      : []
  const path = polygons.flatMap((polygon) => polygon.map(ringPath)).join('')
  if (path) paths[code] = `${paths[code] ?? ''}${path}`
}

const here = dirname(fileURLToPath(import.meta.url))
const destination = resolve(here, '../src/assets/world-paths.json')
await writeFile(destination, `${JSON.stringify(Object.fromEntries(Object.entries(paths).sort()))}\n`)
console.log(`Wrote ${Object.keys(paths).length} country paths to ${destination}`)

function ringPath(ring) {
  const projected = ring.map(([longitude, latitude]) => [
    ((longitude + 180) / 360) * WIDTH,
    ((90 - latitude) / 180) * HEIGHT,
  ])
  const points = simplify(projected, TOLERANCE)
  if (points.length < 3) return ''
  return `M${points.map(([x, y]) => `${round(x)} ${round(y)}`).join('L')}Z`
}

function simplify(points, tolerance) {
  if (points.length <= 4) return points
  const closed = samePoint(points[0], points.at(-1))
  const source = closed ? points.slice(0, -1) : points
  const keep = new Uint8Array(source.length)
  keep[0] = 1
  keep[source.length - 1] = 1
  const stack = [[0, source.length - 1]]
  const squaredTolerance = tolerance * tolerance

  while (stack.length) {
    const [start, end] = stack.pop()
    let furthest = -1
    let maximum = squaredTolerance
    for (let index = start + 1; index < end; index += 1) {
      const distance = segmentDistanceSquared(source[index], source[start], source[end])
      if (distance > maximum) {
        maximum = distance
        furthest = index
      }
    }
    if (furthest >= 0) {
      keep[furthest] = 1
      stack.push([start, furthest], [furthest, end])
    }
  }

  const simplified = source.filter((_, index) => keep[index])
  return closed ? [...simplified, simplified[0]] : simplified
}

function segmentDistanceSquared(point, start, end) {
  let x = start[0]
  let y = start[1]
  const dx = end[0] - x
  const dy = end[1] - y
  if (dx || dy) {
    const t = Math.max(0, Math.min(1, ((point[0] - x) * dx + (point[1] - y) * dy) / (dx * dx + dy * dy)))
    x += dx * t
    y += dy * t
  }
  const offsetX = point[0] - x
  const offsetY = point[1] - y
  return offsetX * offsetX + offsetY * offsetY
}

function samePoint(left, right) {
  return left[0] === right[0] && left[1] === right[1]
}

function round(value) {
  return Math.round(value * 10) / 10
}
