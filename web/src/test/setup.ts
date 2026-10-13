// jsdom has no ResizeObserver; the dashboard panel group needs one to mount. Tests that care about
// resize behaviour stub their own with vi.stubGlobal.
if (typeof window !== 'undefined' && typeof window.ResizeObserver === 'undefined') {
  class ResizeObserverFallback {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  window.ResizeObserver = ResizeObserverFallback as unknown as typeof ResizeObserver
}
