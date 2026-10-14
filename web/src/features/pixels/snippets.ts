export function pixelSnippets(p: { svg_url: string; gif_url: string }, homepage: string) {
  const home = homepage.replace(/\/+$/, '')
  return {
    linkedMarkdown: `[![views](${p.svg_url})](${home}/?ref=badge)`,
    statsMarkdown: `[![views](${appendQuery(p.svg_url, 'style=stats')})](${home}/?ref=badge)`,
    markdown: `![views](${p.svg_url})`,
    invisibleMarkdown: `![](${p.gif_url})`,
    html: `<img src="${p.gif_url}" alt="" width="1" height="1" referrerpolicy="no-referrer-when-downgrade">`,
  }
}

function appendQuery(url: string, query: string): string {
  return `${url}${url.includes('?') ? '&' : '?'}${query}`
}

/** The badge image for a tracking pixel, preserving its tag query parameter. */
export function badgeUrl(gifUrl: string): string {
  const url = new URL(gifUrl)
  url.pathname = url.pathname.replace(/\.gif$/, '.svg')
  return url.toString()
}
