// Latest release tag (`v*`) at build time, injected by Vite; empty when git is unavailable.
declare const __SUPERMETRY_RELEASE_TAG__: string | undefined

export const releaseTag = typeof __SUPERMETRY_RELEASE_TAG__ === 'string' ? __SUPERMETRY_RELEASE_TAG__ : ''

/**
 * The build version shown in the app. Release servers report their tag; development
 * servers report `dev` (or `vdev`), shown as the latest release tag plus "dev".
 */
export function versionLabel(serverVersion: string | undefined, tag: string = releaseTag): string {
  const bare = (serverVersion ?? '').trim().replace(/^v/, '')
  if (bare && bare !== 'dev') return `v${bare}`
  return tag ? `${tag} · dev` : 'dev'
}
