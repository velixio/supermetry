export function BrandLogo({
  className = 'h-7 w-auto',
  mark = false,
  decorative = false,
}: {
  className?: string
  mark?: boolean
  decorative?: boolean
}) {
  if (mark) {
    return (
      <>
        <img alt={decorative ? '' : 'Supermetry'} aria-hidden={decorative || undefined} className={`${className} dark:hidden`} src="/brand/supermetry-mark.svg" />
        <img alt="" aria-hidden="true" className={`${className} hidden dark:block`} src="/brand/supermetry-mark-on-dark.svg" />
      </>
    )
  }
  return (
    <>
      <img alt={decorative ? '' : 'Supermetry'} aria-hidden={decorative || undefined} className={`${className} dark:hidden`} src="/brand/supermetry-logo-on-light.svg" />
      <img alt="" aria-hidden="true" className={`${className} hidden dark:block`} src="/brand/supermetry-logo-on-dark.svg" />
    </>
  )
}
