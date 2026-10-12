type RecordingSize = { width: number; height: number }

type SnapshotNode = {
  type: number
  id: number
  tagName?: string
  attributes?: Record<string, string>
  textContent?: string
  childNodes?: SnapshotNode[]
}

export const desktopReplayId = '1683029196918025731'
export const mobileReplayId = '1683029196918025732'
export const resizingReplayId = '1683029196918025733'

function snapshot(size: RecordingSize, title: string) {
  let id = 1
  const text = (value: string): SnapshotNode => ({ type: 3, id: id++, textContent: value })
  const element = (
    tagName: string,
    attributes: Record<string, string> = {},
    childNodes: SnapshotNode[] = [],
  ): SnapshotNode => ({ type: 2, id: id++, tagName, attributes, childNodes })
  const documentNode: SnapshotNode = { type: 0, id: id++, childNodes: [] }
  const doctype: SnapshotNode = { type: 1, id: id++ }
  const style = element('style', {}, [text(`
    *{box-sizing:border-box}body{margin:0;background:#eef1f6;color:#172033;font-family:system-ui}
    header{height:72px;padding:20px 7%;background:#172033;color:white;font-weight:700}
    main{min-height:${Math.max(300, size.height - 112)}px;padding:8% 7%;background:linear-gradient(135deg,#eef1f6,#dfe4ff)}
    h1{font-size:clamp(28px,6vw,72px);margin:0 0 24px;max-width:12ch}p{font-size:18px;max-width:38ch}
    .metric{display:inline-block;margin-top:32px;padding:20px 28px;border-radius:16px;background:white;border:1px solid #cbd2e1}
    footer{height:40px;padding:10px 7%;background:#d7ddea}
  `)])
  const head = element('head', {}, [style])
  const body = element('body', {}, [
    element('header', {}, [text('Acme Cloud')]),
    element('main', {}, [
      element('h1', {}, [text(title)]),
      element('p', {}, [text('A real replay fixture with a recorded viewport and complete document snapshot.')]),
      element('div', { class: 'metric' }, [text(`${size.width} × ${size.height}`)]),
    ]),
    element('footer', {}, [text('Replay fixture')]),
  ])
  documentNode.childNodes = [doctype, element('html', { lang: 'en' }, [head, body])]
  return documentNode
}

export function replayRecording(size: RecordingSize, title: string): string {
  const timestamp = Date.parse('2026-10-11T14:45:00Z')
  return [
    { type: 4, timestamp, data: { href: 'https://site1.test/', ...size } },
    { type: 2, timestamp: timestamp + 1, data: { node: snapshot(size, title), initialOffset: { left: 0, top: 0 } } },
    { type: 5, timestamp: timestamp + 45_000, data: { tag: 'fixture-end', payload: null } },
  ].map((event) => JSON.stringify(event)).join('\n')
}

export const desktopRecording = replayRecording({ width: 1440, height: 900 }, 'Desktop analytics, clearly framed')
export const mobileRecording = replayRecording({ width: 390, height: 844 }, 'Mobile insight in one view')
export const resizingRecording = `${desktopRecording}\n${JSON.stringify({
  type: 3,
  timestamp: Date.parse('2026-10-11T14:45:00Z') + 500,
  data: { source: 4, width: 390, height: 844 },
})}`
