import { describe, expect, it } from 'vitest'

import { buttonVariants } from './button'

describe('button variants', () => {
  it('keeps icon buttons at least 24px (size-6 = 1.5rem)', () => {
    for (const size of ['icon', 'icon-xs', 'icon-sm', 'icon-lg'] as const) {
      const m = buttonVariants({ size }).match(/(?:^|\s)size-(\d+(?:\.\d+)?)(?:\s|$)/)
      expect(m, size).not.toBeNull()
      expect(Number(m![1]) * 4, size).toBeGreaterThanOrEqual(24)
    }
  })
  it('destructive uses the solid danger tokens', () => {
    expect(buttonVariants({ variant: 'destructive' })).toContain('bg-destructive-solid')
    expect(buttonVariants({ variant: 'destructive' })).toContain('text-destructive-foreground')
  })
})
