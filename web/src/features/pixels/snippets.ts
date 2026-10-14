export function pixelSnippets(p: { svg_url: string; gif_url: string }, homepage: string) {
  const home = homepage.replace(/\/+$/, '')
  return {
    linkedMarkdown: `[![views](${p.svg_url})](${home}/?ref=badge)`,
    statsMarkdown: `[![views](${p.svg_url}?style=stats)](${home}/?ref=badge)`,
    markdown: `![views](${p.svg_url})`,
    invisibleMarkdown: `![](${p.gif_url})`,
    html: `<img src="${p.gif_url}" alt="" width="1" height="1" referrerpolicy="no-referrer-when-downgrade">`,
  }
}
