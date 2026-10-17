import { useEffect, useState } from 'react'

import { loadPublicConfig } from '@/lib/publicConfig'

const defaultHomepage = 'https://supermetry.com'

export function PoweredBySupermetry({ className = '' }: { className?: string }) {
  const [homepage, setHomepage] = useState(defaultHomepage)

  useEffect(() => {
    let active = true
    void loadPublicConfig().then((config) => {
      if (active && typeof config?.homepage_url === 'string') setHomepage(config.homepage_url)
    })
    return () => { active = false }
  }, [])

  return (
    <a className={className} href={homepage} rel="noreferrer" target="_blank">
      Powered by Supermetry
    </a>
  )
}
