import { useEffect, useState } from 'react'

const defaultHomepage = 'https://supermetry.com'

export function PoweredBySupermetry({ className = '' }: { className?: string }) {
  const [homepage, setHomepage] = useState(defaultHomepage)

  useEffect(() => {
    const controller = new AbortController()
    void fetch('/api/private/auth/config', {
      credentials: 'same-origin',
      signal: controller.signal,
    })
      .then(async (response) => {
        if (!response.ok) return
        const config = await response.json() as { homepage_url?: unknown }
        if (typeof config.homepage_url === 'string') setHomepage(config.homepage_url)
      })
      .catch(() => undefined)
    return () => controller.abort()
  }, [])

  return (
    <a className={className} href={homepage} rel="noreferrer" target="_blank">
      Powered by Supermetry
    </a>
  )
}
