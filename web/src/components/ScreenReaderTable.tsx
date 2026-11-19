import type { ComponentProps } from 'react'

/**
 * A data table only screen readers see (the accessible version of a chart).
 * The sr-only box is a div because a table ignores `height: 1px` and
 * `overflow: hidden`: sr-only on the table itself keeps its full height and
 * stretches the page's scroll area with empty space.
 */
export function ScreenReaderTable(props: ComponentProps<'table'>) {
  return (
    <div className="sr-only">
      <table {...props} />
    </div>
  )
}
