import '@testing-library/jest-dom/vitest'

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ReplayStorageForm } from './ReplayStorageForm'

afterEach(cleanup)

describe('ReplayStorageForm', () => {
  it('asks whether existing replays should be migrated and offers both actions', () => {
    render(
      <ReplayStorageForm
        settings={{
          backend: { value: 'disk', locked: false },
          dir: { value: '/var/lib/supermetry/replay', locked: false },
          s3: { value: { endpoint: '', bucket: '', region: '', access_key: '', prefix: '', secret_key_set: false }, locked: false },
        }}
        saving={false}
        onSave={vi.fn()}
      />,
    )

    fireEvent.change(screen.getByLabelText('Replay directory'), { target: { value: '/mnt/replays' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save storage settings' }))

    expect(screen.getByRole('alertdialog', { name: 'Migrate existing replays?' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Migrate in background' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Leave in place' })).toBeVisible()
  })
})
