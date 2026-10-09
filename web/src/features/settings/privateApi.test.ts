import { QueryClient } from '@tanstack/react-query'
import { afterEach, expect, it, vi } from 'vitest'
import { createSessionLifecycle } from '@/lib/session-lifecycle'
import { uploadSiteImport } from './privateApi'

class UploadRequest extends EventTarget {
  static latest: UploadRequest
  upload = new EventTarget()
  status = 201
  responseText = '{"id":12}'
  open = vi.fn()
  setRequestHeader = vi.fn()
  send = vi.fn()
  abort = vi.fn(() => this.dispatchEvent(new Event('abort')))
  constructor() { super(); UploadRequest.latest = this }
}
afterEach(() => vi.unstubAllGlobals())
function setup() {
  vi.stubGlobal('XMLHttpRequest', UploadRequest)
  const session = createSessionLifecycle({ client: new QueryClient(), fetch: vi.fn().mockResolvedValue(new Response(null, { status: 204 })), origin: window.location.origin, cookie: () => '', replace: vi.fn(), broadcast: vi.fn() })
  const progress = vi.fn()
  const start = () => uploadSiteImport(1, 2, 'umami', new File(['fixture'], 'import.zip'), progress, session.registerPrivateTask)
  return { session, progress, start }
}
it.each(['logout', 'switch', 'login'])('aborts uploads on %s and discards late progress/load', async (change) => {
  const { session, progress, start } = setup()
  const result = start()
  const rejected = expect(result).rejects.toMatchObject({ name: 'AbortError' })
  const request = UploadRequest.latest
  request.upload.dispatchEvent(new ProgressEvent('progress', { lengthComputable: true, loaded: 1, total: 2 }))
  expect(progress).toHaveBeenCalledExactlyOnceWith(50)
  if (change === 'logout') await session.requestLogout()
  else if (change === 'switch') session.sessionChanged()
  else session.resetAfterLogin()
  await rejected
  expect(request.abort).toHaveBeenCalledOnce()
  request.upload.dispatchEvent(new ProgressEvent('progress', { lengthComputable: true, loaded: 2, total: 2 }))
  request.dispatchEvent(new Event('load'))
  expect(progress).toHaveBeenCalledTimes(1)
})
it('unregisters completed uploads and keeps organization and CSRF headers', async () => {
  const { session, start } = setup()
  document.cookie = 'panel_csrf=upload-token'
  const result = start()
  const request = UploadRequest.latest
  expect(request.setRequestHeader).toHaveBeenCalledWith('X-Panel-Org', '2')
  expect(request.setRequestHeader).toHaveBeenCalledWith('X-CSRF-Token', 'upload-token')
  request.dispatchEvent(new Event('load'))
  await expect(result).resolves.toEqual({ id: 12 })
  session.resetAfterLogin()
  expect(request.abort).not.toHaveBeenCalled()
})
it('unregisters failed uploads and prevents uploads after logout starts', async () => {
  const { session, start } = setup()
  const result = start()
  const request = UploadRequest.latest
  request.dispatchEvent(new Event('error'))
  await expect(result).rejects.toThrow('Upload failed')
  await session.requestLogout()
  expect(request.abort).not.toHaveBeenCalled()
  await expect(start()).rejects.toMatchObject({ name: 'AbortError' })
  expect(UploadRequest.latest.send).not.toHaveBeenCalled()
})
