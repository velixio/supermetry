import * as React from 'react'

/** True while the viewport is at least `minimum` px wide; same matchMedia pattern as useIsMobile. */
export function useMinWidth(minimum: number): boolean {
  const [matches, setMatches] = React.useState(() => typeof window !== 'undefined' && window.innerWidth >= minimum)

  React.useEffect(() => {
    // Environments without matchMedia (jsdom) keep the first measurement.
    if (typeof window.matchMedia !== 'function') return
    const query = window.matchMedia(`(min-width: ${minimum}px)`)
    const onChange = () => setMatches(window.innerWidth >= minimum)
    query.addEventListener('change', onChange)
    onChange()
    return () => query.removeEventListener('change', onChange)
  }, [minimum])

  return matches
}
