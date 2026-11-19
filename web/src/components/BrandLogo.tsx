import { useId } from 'react'

import { cn } from '@/lib/utils'

/** Mark sizes in px: xs and sm use the simplified two-wave artwork, md and lg the full three-wave one. */
const MARK_PX = { xs: 16, sm: 20, md: 24, lg: 32 } as const
export type BrandLogoSize = keyof typeof MARK_PX

// Artwork is drawn on a 32 (full) / 16 (simple) unit grid, paths in absolute coordinates, no transforms.
// Literal class names so Tailwind can see them.
const STOPS: ReadonlyArray<readonly [string, string]> = [
  ['0', '[stop-color:#6d3cf0] dark:[stop-color:#8a5cff]'],
  ['.45', '[stop-color:#1f6fe8] dark:[stop-color:#2f8bff]'],
  ['.8', '[stop-color:#0a93b8] dark:[stop-color:#6ff6ff]'],
  ['1', '[stop-color:#0f9a6c] dark:[stop-color:#7dffc4]'],
]

const FULL = {
  box: 32,
  gradient: [2.3, 23.58, 29.8, 4.14],
  waves: [
    { d: 'M2.3 21.64C8.69 21.64 10.08 12.19 16.47 12.47S25.49 4.69 29.8 4', w: 2.6, o: 1 },
    { d: 'M2.3 24.83C8.69 24.83 10.08 15.39 16.47 15.67S25.49 7.89 29.8 7.19', w: 1.9, o: 0.9 },
    { d: 'M2.3 28.03C8.69 28.03 10.08 18.58 16.47 18.86S25.49 11.08 29.8 10.39', w: 1.4, o: 0.75 },
  ],
}
const SIMPLE = {
  box: 16,
  gradient: [1.9, 11.77, 14.49, 2.86],
  waves: [
    { d: 'M1.9 10.88C4.83 10.88 5.46 6.55 8.39 6.68S12.52 3.12 14.49 2.8', w: 2.5, o: 1 },
    { d: 'M1.9 13.8C4.83 13.8 5.46 9.48 8.39 9.61S12.52 6.04 14.49 5.73', w: 1.75, o: 0.9 },
  ],
}

// "Supermetry" in Open Sans 600, converted to outlines. 103 x 14 units, baseline y=23 on the 32-unit lockup grid.
const WORDMARK = 'M45.68 19.32Q45.68 21.13 44.38 22.16Q43.07 23.19 40.77 23.19Q38.47 23.19 37 22.47V20.37Q37.93 20.81 38.97 21.06Q40.02 21.31 40.92 21.31Q42.23 21.31 42.85 20.81Q43.49 20.31 43.49 19.47Q43.49 18.7 42.91 18.18Q42.33 17.65 40.53 16.92Q38.68 16.17 37.92 15.21Q37.16 14.24 37.16 12.89Q37.16 11.19 38.36 10.22Q39.57 9.24 41.6 9.24Q43.55 9.24 45.48 10.1L44.77 11.9Q42.97 11.14 41.55 11.14Q40.47 11.14 39.91 11.62Q39.36 12.08 39.36 12.85Q39.36 13.38 39.58 13.76Q39.8 14.13 40.31 14.46Q40.82 14.8 42.15 15.35Q43.64 15.97 44.34 16.51Q45.03 17.04 45.36 17.72Q45.68 18.4 45.68 19.32ZM54.81 23L54.51 21.65H54.4Q53.94 22.37 53.1 22.78Q52.26 23.19 51.19 23.19Q49.32 23.19 48.4 22.26Q47.48 21.33 47.48 19.45V12.74H49.68V19.07Q49.68 20.24 50.17 20.84Q50.65 21.42 51.68 21.42Q53.05 21.42 53.69 20.61Q54.34 19.78 54.34 17.85V12.74H56.53V23ZM64.34 23.19Q62.39 23.19 61.32 21.78H61.19Q61.32 23.08 61.32 23.36V27.56H59.14V12.74H60.9Q60.97 13.03 61.21 14.11H61.32Q62.34 12.55 64.38 12.55Q66.3 12.55 67.37 13.95Q68.44 15.34 68.44 17.85Q68.44 20.37 67.35 21.78Q66.26 23.19 64.34 23.19ZM63.81 14.33Q62.51 14.33 61.91 15.1Q61.32 15.86 61.32 17.53V17.85Q61.32 19.73 61.91 20.57Q62.51 21.4 63.85 21.4Q64.98 21.4 65.59 20.48Q66.21 19.55 66.21 17.83Q66.21 16.11 65.59 15.23Q64.99 14.33 63.81 14.33ZM74.96 23.19Q72.56 23.19 71.21 21.79Q69.86 20.39 69.86 17.94Q69.86 15.43 71.12 13.99Q72.37 12.55 74.56 12.55Q76.59 12.55 77.77 13.79Q78.95 15.02 78.95 17.18V18.36H72.11Q72.16 19.85 72.92 20.66Q73.68 21.46 75.06 21.46Q75.97 21.46 76.75 21.29Q77.54 21.12 78.44 20.72V22.49Q77.64 22.87 76.82 23.03Q76.01 23.19 74.96 23.19ZM74.56 14.21Q73.52 14.21 72.89 14.86Q72.27 15.52 72.15 16.78H76.8Q76.78 15.51 76.19 14.86Q75.6 14.21 74.56 14.21ZM86.13 12.55Q86.79 12.55 87.21 12.65L87 14.68Q86.54 14.57 86.03 14.57Q84.73 14.57 83.91 15.42Q83.1 16.27 83.1 17.64V23H80.92V12.74H82.63L82.92 14.55H83.03Q83.54 13.63 84.36 13.09Q85.18 12.55 86.13 12.55ZM97.26 23H95.07V16.67Q95.07 15.49 94.63 14.92Q94.18 14.33 93.24 14.33Q91.97 14.33 91.38 15.16Q90.8 15.98 90.8 17.89V23H88.61V12.74H90.32L90.63 14.08H90.74Q91.17 13.35 91.97 12.95Q92.79 12.55 93.76 12.55Q96.13 12.55 96.9 14.17H97.05Q97.5 13.41 98.33 12.98Q99.15 12.55 100.22 12.55Q102.06 12.55 102.89 13.48Q103.74 14.41 103.74 16.31V23H101.56V16.67Q101.56 15.49 101.1 14.92Q100.66 14.33 99.71 14.33Q98.44 14.33 97.85 15.13Q97.26 15.92 97.26 17.56ZM110.76 23.19Q108.37 23.19 107.01 21.79Q105.67 20.39 105.67 17.94Q105.67 15.43 106.92 13.99Q108.17 12.55 110.36 12.55Q112.39 12.55 113.57 13.79Q114.75 15.02 114.75 17.18V18.36H107.91Q107.96 19.85 108.72 20.66Q109.48 21.46 110.86 21.46Q111.77 21.46 112.55 21.29Q113.34 21.12 114.24 20.72V22.49Q113.44 22.87 112.63 23.03Q111.81 23.19 110.76 23.19ZM110.36 14.21Q109.32 14.21 108.69 14.86Q108.07 15.52 107.95 16.78H112.61Q112.59 15.51 111.99 14.86Q111.4 14.21 110.36 14.21ZM120.55 21.42Q121.35 21.42 122.15 21.17V22.81Q121.78 22.97 121.21 23.07Q120.64 23.19 120.03 23.19Q116.93 23.19 116.93 19.92V14.39H115.53V13.43L117.03 12.63L117.78 10.46H119.12V12.74H122.04V14.39H119.12V19.88Q119.12 20.67 119.51 21.05Q119.91 21.42 120.55 21.42ZM128.9 12.55Q129.56 12.55 129.99 12.65L129.77 14.68Q129.31 14.57 128.81 14.57Q127.5 14.57 126.68 15.42Q125.88 16.27 125.88 17.64V23H123.7V12.74H125.4L125.69 14.55H125.8Q126.31 13.63 127.13 13.09Q127.95 12.55 128.9 12.55ZM129.83 12.74H132.2L134.29 18.56Q134.76 19.8 134.92 20.89H135Q135.08 20.38 135.3 19.66Q135.53 18.93 137.66 12.74H140.02L135.63 24.36Q134.43 27.56 131.64 27.56Q130.91 27.56 130.23 27.41V25.68Q130.72 25.79 131.35 25.79Q132.93 25.79 133.57 23.96L133.95 23Z'

function Waves({ art, id }: { art: typeof FULL; id: string }) {
  const [x1, y1, x2, y2] = art.gradient
  return (
    <>
      <defs>
        <linearGradient gradientUnits="userSpaceOnUse" id={id} x1={x1} x2={x2} y1={y1} y2={y2}>
          {STOPS.map(([offset, cls]) => (
            <stop key={offset} className={cls} offset={offset} />
          ))}
        </linearGradient>
      </defs>
      <g fill="none" stroke={`url(#${id})`} strokeLinecap="round">
        {art.waves.map((wave) => (
          <path key={wave.d} d={wave.d} opacity={wave.o === 1 ? undefined : wave.o} strokeWidth={wave.w} />
        ))}
      </g>
    </>
  )
}

type BrandLogoProps = {
  /** Extra classes. When no `size` is given and no h-/size- class is passed, the default is h-7 for the logo. */
  className?: string
  /** Render only the mark. */
  mark?: boolean
  /** Hide from assistive tech when the surrounding element already names the brand. */
  decorative?: boolean
  /** Mark size preset: xs 16px, sm 20px, md 24px, lg 32px. The full logo is as tall as the mark. */
  size?: BrandLogoSize
}

export function BrandLogo({ className, mark = false, decorative = false, size }: BrandLogoProps) {
  const gid = `brand-${useId().replace(/[^a-zA-Z0-9]/g, '')}`
  const px = size ? MARK_PX[size] : undefined
  const a11y = decorative ? { 'aria-hidden': true as const } : { role: 'img' as const, 'aria-label': 'Supermetry' }
  const common = { ...a11y, focusable: 'false' as const, shapeRendering: 'geometricPrecision' as const, xmlns: 'http://www.w3.org/2000/svg' }

  if (mark) {
    // The simplified artwork is sturdier up to 20px; from 24px the three-wave mark reads fine.
    const art = px !== undefined && px <= 20 ? SIMPLE : FULL
    return (
      <svg
        {...common}
        className={cn('shrink-0', !className && !px && 'size-5', className)}
        height={px}
        viewBox={`0 0 ${art.box} ${art.box}`}
        width={px}
      >
        <Waves art={art} id={gid} />
      </svg>
    )
  }
  return (
    <svg
      {...common}
      className={cn('shrink-0', !className && !px && 'h-7', 'w-auto', className)}
      height={px}
      viewBox="0 0 140 32"
      width={px ? (px * 140) / 32 : undefined}
    >
      <Waves art={FULL} id={gid} />
      <path d={WORDMARK} fill="currentColor" />
    </svg>
  )
}
