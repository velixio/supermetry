import { Component, type ReactNode } from 'react'

/**
 * Contains a failed palette chunk load so one click cannot replace the whole shell with the
 * route error screen. The parent reports the failure and remounts this boundary (via `key`).
 */
export class PaletteBoundary extends Component<{ onError: (error: unknown) => void; children: ReactNode }, { failed: boolean }> {
  state = { failed: false }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  componentDidCatch(error: unknown) {
    this.props.onError(error)
  }

  render() {
    return this.state.failed ? null : this.props.children
  }
}
