import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// @ts-expect-error jsdom does not publish TypeScript declarations.
import { JSDOM } from 'jsdom'
import { describe, expect, it } from 'vitest'

type EventPayload = {
  name: string
  props: Record<string, string>
  url: string
  clicks?: [number, number][]
  doc_h?: number
  vw?: number
}

// Exercise the checked-in artifact browsers receive, including minification.
const tracker = readFileSync(
  resolve('../internal/tracker/tracker.full.min.js'),
  'utf8',
)

type ObserverHarness = {
  emit: (type: string, entries: Record<string, unknown>[]) => void
  queue: (type: string, entries: Record<string, unknown>[]) => void
  observed: Array<{ type?: string; buffered?: boolean; durationThreshold?: number }>
}

function load(attributes = '', body = '', withObservers = false) {
  const dom = new JSDOM(`<!doctype html><html><head>${body}</head><body></body></html>`, {
    runScripts: 'dangerously',
    url: 'https://site.example/start?private=yes',
  })
  const payloads: EventPayload[] = []
  class BodyBlob {
    body: string
    constructor(parts: unknown[]) {
      this.body = parts.join('')
    }
  }
  Object.defineProperty(dom.window, 'Blob', { configurable: true, value: BodyBlob })
  Object.defineProperty(dom.window.navigator, 'sendBeacon', {
    configurable: true,
    value: (_url: string, data: BodyBlob | string) => {
      payloads.push(JSON.parse(typeof data === 'string' ? data : data.body) as EventPayload)
      return true
    },
  })
  const observerHarness: ObserverHarness = { emit: () => {}, queue: () => {}, observed: [] }
  if (withObservers) {
    const observers: Array<{
      callback: (list: { getEntries: () => Record<string, unknown>[] }) => void
      disconnected: boolean
      pending: Record<string, unknown>[]
      type?: string
    }> = []
    class MockPerformanceObserver {
      callback: (list: { getEntries: () => Record<string, unknown>[] }) => void
      disconnected = false
      pending: Record<string, unknown>[] = []
      type?: string
      constructor(callback: (list: { getEntries: () => Record<string, unknown>[] }) => void) {
        this.callback = callback
        observers.push(this)
      }
      observe(options: { type?: string; buffered?: boolean; durationThreshold?: number }) {
        this.type = options.type
        observerHarness.observed.push({ ...options })
      }
      takeRecords() { return this.pending.splice(0) }
      disconnect() { this.disconnected = true }
    }
    Object.defineProperty(dom.window, 'PerformanceObserver', {
      configurable: true,
      value: MockPerformanceObserver,
    })
    observerHarness.emit = (type, entries) => {
      for (const observer of observers) {
        if (!observer.disconnected && observer.type === type) {
          observer.callback({ getEntries: () => entries })
        }
      }
    }
    observerHarness.queue = (type, entries) => {
      for (const observer of observers) {
        if (!observer.disconnected && observer.type === type) {
          observer.pending.push(...entries)
        }
      }
    }
  }
  const script = dom.window.document.createElement('script')
  script.setAttribute('data-site', 'site-public')
  script.setAttribute('data-allow-localhost', '')
  for (const match of attributes.matchAll(/([\w-]+)(?:="([^"]*)")?/g)) {
    script.setAttribute(match[1], match[2] ?? '')
  }
  script.textContent = tracker
  dom.window.document.head.append(script)
  return { dom, payloads, observers: observerHarness }
}

function event(payloads: EventPayload[], name: string) {
  return payloads.find((payload) => payload.name === name)
}

describe('full tracker', () => {
  it('captures normalized click coordinates and page dimensions on pagehide', () => {
    const { dom, payloads } = load('data-heatmap="true"')
    Object.defineProperty(dom.window.document.documentElement, 'scrollWidth', { configurable: true, value: 2000 })
    Object.defineProperty(dom.window.document.documentElement, 'scrollHeight', { configurable: true, value: 4321 })
    Object.defineProperty(dom.window, 'innerWidth', { configurable: true, value: 1280 })
    const click = new dom.window.MouseEvent('click', { bubbles: true, clientX: 500 })
    Object.defineProperty(click, 'pageY', { configurable: true, value: 1234.6 })

    dom.window.document.body.dispatchEvent(click)
    dom.window.dispatchEvent(new dom.window.Event('pagehide'))

    expect(event(payloads, 'heatmap')).toMatchObject({
      clicks: [[250, 1235]],
      doc_h: 4321,
      vw: 1280,
    })
  })

  it('clamps horizontal coordinates and captures at most 200 clicks per page', () => {
    const { dom, payloads } = load('data-heatmap="true"')
    Object.defineProperty(dom.window.document.documentElement, 'scrollWidth', { configurable: true, value: 1000 })
    for (let index = 0; index < 205; index++) {
      const click = new dom.window.MouseEvent('click', { bubbles: true, clientX: index === 0 ? -20 : 1200 })
      Object.defineProperty(click, 'pageY', {
        configurable: true,
        value: index === 0 ? -20 : index === 199 ? 123456 : index,
      })
      dom.window.document.body.dispatchEvent(click)
    }

    dom.window.dispatchEvent(new dom.window.Event('pagehide'))

    const heatmap = event(payloads, 'heatmap')
    expect(heatmap?.clicks).toHaveLength(200)
    expect(heatmap?.clicks?.[0]).toEqual([0, 0])
    expect(heatmap?.clicks?.[199]).toEqual([1000, 100000])
  })

  it('bounds document and viewport dimensions for ingestion', () => {
    const { dom, payloads } = load('data-heatmap="true"')
    Object.defineProperty(dom.window.document.documentElement, 'scrollHeight', { configurable: true, value: 123456 })
    Object.defineProperty(dom.window, 'innerWidth', { configurable: true, value: 12345 })
    dom.window.document.body.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }))

    dom.window.dispatchEvent(new dom.window.Event('pagehide'))

    expect(event(payloads, 'heatmap')).toMatchObject({ doc_h: 100000, vw: 10000 })
  })

  it('uses the core transport fallback when sendBeacon is unavailable', () => {
    const { dom, payloads } = load('data-heatmap="true"')
    Object.defineProperty(dom.window.navigator, 'sendBeacon', { configurable: true, value: undefined })
    Object.defineProperty(dom.window, 'fetch', {
      configurable: true,
      value: (_url: string, init: { body: string }) => {
        payloads.push(JSON.parse(init.body) as EventPayload)
        return Promise.resolve({})
      },
    })
    dom.window.document.body.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }))

    dom.window.dispatchEvent(new dom.window.Event('pagehide'))

    expect(event(payloads, 'heatmap')?.clicks).toEqual([[0, 0]])
  })

  it('flushes each page heatmap once across SPA navigation and pagehide', () => {
    const { dom, payloads } = load('data-heatmap="true"')
    Object.defineProperty(dom.window.document.documentElement, 'scrollWidth', { configurable: true, value: 1000 })
    dom.window.document.body.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, clientX: 100 }))

    dom.window.history.pushState({}, '', '/next')
    dom.window.document.body.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, clientX: 900 }))
    dom.window.dispatchEvent(new dom.window.Event('pagehide'))
    dom.window.dispatchEvent(new dom.window.Event('pagehide'))

    const heatmaps = payloads.filter((payload) => payload.name === 'heatmap')
    expect(heatmaps).toHaveLength(2)
    expect(heatmaps.map((payload) => payload.url)).toEqual([
      'https://site.example/start?private=yes',
      'https://site.example/next',
    ])
    expect(heatmaps.map((payload) => payload.clicks)).toEqual([[[100, 0]], [[900, 0]]])
  })

  it('does not capture heatmaps by default', () => {
    const { dom, payloads } = load()
    dom.window.document.body.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, clientX: 100 }))
    dom.window.dispatchEvent(new dom.window.Event('pagehide'))

    expect(event(payloads, 'heatmap')).toBeUndefined()
  })

  it('tracks outbound clicks and middle clicks without query strings', () => {
    const { dom, payloads } = load()
    const link = dom.window.document.createElement('a')
    link.href = 'https://elsewhere.example/page?secret=yes#section'
    dom.window.document.body.append(link)

    link.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }))
    link.dispatchEvent(new dom.window.MouseEvent('auxclick', { bubbles: true, button: 1 }))

    const outbound = payloads.filter((payload) => payload.name === 'Outbound Link: Click')
    expect(outbound).toHaveLength(2)
    expect(outbound[0].props.url).toBe('https://elsewhere.example/page#section')
  })

  it.each([
    ['', ['/report.pdf'], ['https://site.example/report.pdf']],
    ['data-download-ext="log,tar"', ['/report.pdf', '/archive.TAR?token=secret', '/notes.log'], [
      'https://site.example/archive.TAR',
      'https://site.example/notes.log',
    ]],
  ])('tracks downloads with extension config %s', (attributes, hrefs, expected) => {
    const { dom, payloads } = load(attributes)
    for (const href of hrefs) {
      const link = dom.window.document.createElement('a')
      link.href = href
      dom.window.document.body.append(link)
      link.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }))
    }

    const downloads = payloads.filter((payload) => payload.name === 'File Download')
    expect(downloads.map((payload) => payload.props.url)).toEqual(expected)
  })

  it('tracks marked 404 pages after the pageview', () => {
    const { payloads } = load('', '<meta name="panel:404">')

    expect(payloads.map((payload) => payload.name).slice(0, 2)).toEqual(['pageview', '404'])
    expect(event(payloads, '404')?.props.path).toBe('/start')
  })

  it('tracks click and form attributes with lowercased bounded props', () => {
    const { dom, payloads } = load()
    const button = dom.window.document.createElement('button')
    button.setAttribute('data-panel-event', 'Signup')
    button.setAttribute('data-panel-prop-Plan', 'Pro')
    for (let index = 0; index < 31; index++) {
      button.setAttribute(`data-panel-prop-p${index}`, 'x')
    }
    const child = dom.window.document.createElement('span')
    button.append(child)
    dom.window.document.body.append(button)
    child.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }))

    const form = dom.window.document.createElement('form')
    form.setAttribute('data-panel-event', 'Lead')
    dom.window.document.body.append(form)
    form.dispatchEvent(new dom.window.Event('submit', { bubbles: true }))

    expect(event(payloads, 'Signup')?.props.plan).toBe('Pro')
    expect(Object.keys(event(payloads, 'Signup')?.props ?? {})).toHaveLength(30)
    expect(event(payloads, 'Lead')).toBeDefined()
  })

  it.each([
    ['data-outbound="false"', 'Outbound Link: Click', 'https://away.example/path'],
    ['data-downloads="false"', 'File Download', '/report.pdf'],
    ['data-clicks="false"', 'Signup', '#'],
  ])('honors %s', (attributes, blockedEvent, href) => {
    const { dom, payloads } = load(attributes)
    const link = dom.window.document.createElement('a')
    link.href = href
    link.setAttribute('data-panel-event', 'Signup')
    dom.window.document.body.append(link)
    link.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }))

    expect(event(payloads, blockedEvent)).toBeUndefined()
  })

  it('honors data-404=false for both 404 markers', () => {
    const { payloads } = load('data-404="false" data-404-page', '<meta name="panel:404">')

    expect(event(payloads, '404')).toBeUndefined()
  })

  it('does not run extensions when the core tracker exits', () => {
    const { dom, payloads } = load('data-site=""')
    const link = dom.window.document.createElement('a')
    link.href = 'https://away.example/path'
    dom.window.document.body.append(link)
    link.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }))

    expect(payloads).toHaveLength(0)
  })

  it('reports LCP, maximum CLS session window, and worst observed interaction once', () => {
    const { dom, payloads, observers } = load('', '', true)
    expect(observers.observed).toContainEqual({
      type: 'event',
      buffered: true,
      durationThreshold: 40,
    })
    observers.emit('largest-contentful-paint', [{ startTime: 1234 }, { startTime: 2789 }])
    observers.emit('layout-shift', [
      { startTime: 0, value: 0.05, hadRecentInput: false },
      { startTime: 500, value: 0.08, hadRecentInput: false },
      { startTime: 2000, value: 0.1, hadRecentInput: false },
      { startTime: 2100, value: 0.9, hadRecentInput: true },
    ])
    observers.emit('event', [
      { interactionId: 1, duration: 180 },
      { interactionId: 2, duration: 540 },
      { interactionId: 0, duration: 900 },
    ])

    Object.defineProperty(dom.window.document, 'hidden', { configurable: true, value: true })
    dom.window.document.dispatchEvent(new dom.window.Event('visibilitychange'))
    dom.window.dispatchEvent(new dom.window.Event('pagehide'))

    const vitals = payloads.filter((payload) => payload.name === 'Web Vitals')
    expect(vitals).toHaveLength(1)
    expect(vitals[0].props).toEqual({ lcp: '2789', cls: '130', inp: '540' })
  })

  it('stops LCP at first input and omits metrics that were not observed', () => {
    const { dom, payloads, observers } = load('', '', true)
    observers.emit('largest-contentful-paint', [{ startTime: 1900 }])
    dom.window.document.dispatchEvent(new dom.window.Event('pointerdown', { bubbles: true }))
    observers.emit('largest-contentful-paint', [{ startTime: 8000 }])

    Object.defineProperty(dom.window.document, 'hidden', { configurable: true, value: true })
    dom.window.document.dispatchEvent(new dom.window.Event('visibilitychange'))

    expect(event(payloads, 'Web Vitals')?.props).toEqual({ lcp: '1900' })
  })

  it('drains queued observer records before reporting', () => {
    const { dom, payloads, observers } = load('', '', true)
    observers.queue('largest-contentful-paint', [{ startTime: 2100 }])
    observers.queue('layout-shift', [{ startTime: 400, value: 0.12, hadRecentInput: false }])
    observers.queue('event', [{ interactionId: 1, duration: 260 }])

    Object.defineProperty(dom.window.document, 'hidden', { configurable: true, value: true })
    dom.window.document.dispatchEvent(new dom.window.Event('visibilitychange'))

    expect(event(payloads, 'Web Vitals')?.props).toEqual({
      lcp: '2100',
      cls: '120',
      inp: '260',
    })
  })

  it('flushes and resets Web Vitals for SPA navigation without replaying buffered entries', () => {
    const { dom, payloads, observers } = load('', '', true)
    observers.emit('largest-contentful-paint', [{ startTime: 900 }])
    observers.emit('layout-shift', [{ startTime: 300, value: 0.3, hadRecentInput: false }])

    observers.observed.length = 0
    dom.window.history.pushState({}, '', '/next')
    // Soft navigations have no LCP and must not replay the previous page's entries.
    expect(observers.observed.map((o) => o.type)).not.toContain('largest-contentful-paint')
    expect(observers.observed.every((o) => !o.buffered)).toBe(true)
    observers.emit('layout-shift', [{ startTime: 6000, value: 0.05, hadRecentInput: false }])
    Object.defineProperty(dom.window.document, 'hidden', { configurable: true, value: true })
    dom.window.document.dispatchEvent(new dom.window.Event('visibilitychange'))

    expect(payloads.filter((payload) => payload.name === 'Web Vitals').map((payload) => payload.props)).toEqual([
      { lcp: '900', cls: '300' },
      { cls: '50' },
    ])
  })

  it('stops previous observers on SPA navigation after an earlier hidden flush', () => {
    const { dom, payloads, observers } = load('', '', true)
    observers.emit('event', [{ interactionId: 1, duration: 300 }])
    Object.defineProperty(dom.window.document, 'hidden', { configurable: true, value: true })
    dom.window.document.dispatchEvent(new dom.window.Event('visibilitychange'))
    Object.defineProperty(dom.window.document, 'hidden', { configurable: true, value: false })
    dom.window.document.dispatchEvent(new dom.window.Event('visibilitychange'))

    dom.window.history.pushState({}, '', '/next')
    observers.emit('event', [{ interactionId: 2, duration: 120 }])
    Object.defineProperty(dom.window.document, 'hidden', { configurable: true, value: true })
    dom.window.document.dispatchEvent(new dom.window.Event('visibilitychange'))

    expect(payloads.filter((payload) => payload.name === 'Web Vitals').map((payload) => payload.props)).toEqual([
      { inp: '300' },
      { inp: '120' },
    ])
  })

  it('sends no Web Vitals event when nothing was measured', () => {
    const { dom, payloads } = load('', '', true)
    Object.defineProperty(dom.window.document, 'hidden', { configurable: true, value: true })
    dom.window.document.dispatchEvent(new dom.window.Event('visibilitychange'))

    expect(event(payloads, 'Web Vitals')).toBeUndefined()
  })

  it('does not throw without PerformanceObserver or with unsupported entry types', () => {
    const plain = load()
    Object.defineProperty(plain.dom.window.document, 'hidden', { configurable: true, value: true })
    expect(() => {
      plain.dom.window.document.dispatchEvent(new plain.dom.window.Event('visibilitychange'))
      plain.dom.window.history.pushState({}, '', '/next')
    }).not.toThrow()
    expect(event(plain.payloads, 'Web Vitals')).toBeUndefined()

    const errors: unknown[] = []
    const { dom, payloads } = load('', '', true)
    dom.window.addEventListener('error', (e: { error: unknown }) => errors.push(e.error))
    const PO = dom.window.PerformanceObserver as { prototype: Record<string, unknown> }
    PO.prototype.observe = () => { throw new TypeError('unsupported') }
    PO.prototype.takeRecords = undefined
    dom.window.history.pushState({}, '', '/again')
    Object.defineProperty(dom.window.document, 'hidden', { configurable: true, value: true })
    dom.window.document.dispatchEvent(new dom.window.Event('visibilitychange'))
    expect(errors).toEqual([])
    expect(event(payloads, 'Web Vitals')).toBeUndefined()
  })

  it('does not observe or report Web Vitals when opted out', () => {
    const { dom, payloads, observers } = load('data-vitals="false"', '', true)
    Object.defineProperty(dom.window.document, 'hidden', { configurable: true, value: true })
    dom.window.document.dispatchEvent(new dom.window.Event('visibilitychange'))

    expect(observers.observed).toEqual([])
    expect(event(payloads, 'Web Vitals')).toBeUndefined()
  })
})
