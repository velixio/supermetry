import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiProblem } from './apiProblem'
import { isReauthRequired } from './reauth'
import { useReauth } from './useReauth'

afterEach(cleanup)

const needsReauth = () => new ApiProblem(403, { code: 'reauth_required' })

function Harness({ run, submit }: { run: () => Promise<string>; submit: (v: { password: string; totp_code?: string }) => Promise<void> }) {
  const { withReauth, reauthDialog } = useReauth(submit)
  return (
    <>
      <button onClick={() => withReauth(run).then((v) => { document.title = v }, (e) => { document.title = `rejected:${(e as ApiProblem).code}` })}>Go</button>
      {reauthDialog}
    </>
  )
}

describe('useReauth', () => {
  it('detects the reauth code', () => {
    expect(isReauthRequired(needsReauth())).toBe(true)
    expect(isReauthRequired(new ApiProblem(403, { code: 'forbidden' }))).toBe(false)
  })

  it('opens the dialog on reauth_required and retries once after success', async () => {
    const run = vi.fn().mockRejectedValueOnce(needsReauth()).mockResolvedValueOnce('done')
    const submit = vi.fn().mockResolvedValue(undefined)
    render(<Harness run={run} submit={submit} />)
    fireEvent.click(screen.getByRole('button', { name: 'Go' }))
    fireEvent.change(await screen.findByLabelText('Password'), { target: { value: 'hunter2' } })
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(document.title).toBe('done'))
    expect(submit).toHaveBeenCalledWith({ password: 'hunter2' })
    expect(run).toHaveBeenCalledTimes(2)
  })

  it('shows an error and keeps the dialog open on a wrong password; cancel rejects with the original error', async () => {
    const run = vi.fn().mockRejectedValue(needsReauth())
    const submit = vi.fn().mockRejectedValue(new ApiProblem(401, {}))
    render(<Harness run={run} submit={submit} />)
    fireEvent.click(screen.getByRole('button', { name: 'Go' }))
    fireEvent.change(await screen.findByLabelText('Password'), { target: { value: 'nope' } })
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(document.title).toBe('rejected:reauth_required'))
    expect(run).toHaveBeenCalledTimes(1)
  })

  it('asks for the authentication code when the server requires it', async () => {
    const run = vi.fn().mockRejectedValueOnce(needsReauth()).mockResolvedValueOnce('ok')
    const submit = vi.fn().mockRejectedValueOnce(new ApiProblem(401, { code: 'totp_required' })).mockResolvedValueOnce(undefined)
    render(<Harness run={run} submit={submit} />)
    fireEvent.click(screen.getByRole('button', { name: 'Go' }))
    fireEvent.change(await screen.findByLabelText('Password'), { target: { value: 'pw' } })
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    fireEvent.change(await screen.findByLabelText('Authentication code'), { target: { value: '123456' } })
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(document.title).toBe('ok'))
    expect(submit).toHaveBeenLastCalledWith({ password: 'pw', totp_code: '123456' })
  })
})
