/** The second key of each "go to" chord (`g` then the letter), by navigation suffix. */
export const NAV_CHORDS: Record<string, string> = {
  '': 'd',
  '/events': 'e',
  '/sessions': 's',
  '/realtime': 'r',
  '/replays': 'p',
  '/journeys': 'j',
  '/retention': 't',
  '/funnels': 'f',
  '/goals': 'o',
  '/revenue': 'v',
  '/users': 'u',
  '/issues': 'i',
  '/links': 'l',
  '/health': 'h',
}

/** Settings: `g ,` opens project settings (organization settings when there is no project). */
export const SETTINGS_CHORD = ','

export const chordLabel = (key: string) => `G ${key.toUpperCase()}`
