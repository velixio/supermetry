type ReplayNode = {
  attributes?: Record<string, unknown>
  childNodes?: ReplayNode[]
}

export async function rehydrateCSS<T>(
  events: T,
  fetchCSS: (hash: string) => Promise<string>,
): Promise<T> {
  const fetched = new Map<string, Promise<string>>()
  const nodes: ReplayNode[] = []
  collectNodes(events, nodes)

  await Promise.all(
    nodes.map(async (node) => {
      const hash = node.attributes?._cssRef
      if (typeof hash !== 'string') return
      let css = fetched.get(hash)
      if (!css) {
        css = fetchCSS(hash)
        fetched.set(hash, css)
      }
      // A stylesheet that cannot be fetched only loses that styling; it must
      // not stop the whole replay from playing.
      const text = await css.catch(() => undefined)
      if (text !== undefined) node.attributes!._cssText = text
      delete node.attributes!._cssRef
    }),
  )
  return events
}

function collectNodes(value: unknown, nodes: ReplayNode[]) {
  if (!value || typeof value !== 'object') return
  if (Array.isArray(value)) {
    for (const item of value) collectNodes(item, nodes)
    return
  }
  const object = value as Record<string, unknown>
  if (object.attributes && typeof object.attributes === 'object') {
    nodes.push(object as ReplayNode)
  }
  for (const child of Object.values(object)) collectNodes(child, nodes)
}
