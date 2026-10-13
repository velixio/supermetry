import { countryLabel } from '@/lib/country'
import { ui } from '@/messages/ui'

function screenSizeLabel(value: string): string | undefined {
  const labels: Record<string, string> = {
    mobile: ui.breakdown.mobile, tablet: ui.breakdown.tablet, laptop: ui.breakdown.laptop,
    desktop: ui.breakdown.desktop, large: ui.breakdown.large,
  }
  return labels[value]
}

/** The label a breakdown row shows for a raw dimension value. */
export function breakdownLabel(dimension: string, value: string): string {
  if (value === '') return ui.breakdown.unknown
  if (dimension === 'country') return countryLabel(value)
  if (dimension === 'screen_size') return screenSizeLabel(value) ?? value
  return value
}
