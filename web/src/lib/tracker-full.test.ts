import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// @ts-expect-error jsdom does not publish TypeScript declarations.
import { JSDOM } from 'jsdom'
import { describe, expect, it } from 'vitest'

type EventPayload = {
  name: string
  props: Record<string, string>
}

const tracker = [
  readFileSync(resolve('../internal/tracker/tracker.js'), 'utf8'),
  readFileSync(resolve('../internal/tracker/tracker.extensions.js'), 'utf8'),
].join('\n')

function load(attributes = '', body = '') {
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
    value: (_url: string, blob: BodyBlob) => {
      payloads.push(JSON.parse(blob.body) as EventPayload)
      return true
    },
  })
  const script = dom.window.document.createElement('script')
  script.setAttribute('data-site', 'site-public')
  script.setAttribute('data-allow-localhost', '')
  for (const match of attributes.matchAll(/([\w-]+)(?:="([^"]*)")?/g)) {
    script.setAttribute(match[1], match[2] ?? '')
  }
  script.textContent = tracker
  dom.window.document.head.append(script)
  return { dom, payloads }
}

function event(payloads: EventPayload[], name: string) {
  return payloads.find((payload) => payload.name === name)
}

describe('full tracker', () => {
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
})
