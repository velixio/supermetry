import { registerCatalogue } from '@/lib/i18n'

// Strings of the page context panel (right-hand side panel). Split from ui.ts so the initial bundle does not carry them.
// Read them when rendering, never at module scope.
export const contextPanel = {
  label: 'Page context',
  shortcutName: 'Page context',
  sections: { quick: 'Quick access', related: 'Related', recommended: 'Suggested', recent: 'Recent' },
  savedDashboard: (name: string) => `Dashboard: ${name}`,
  savedSegment: (name: string) => `Segment: ${name}`,
  recommend: {
    install: 'Install the tracker',
    createGoal: 'Create a goal',
    recordingOff: 'Turn on recording',
    rejected: 'Review rejected events',
  },
  labels: {
    dashboard: 'Dashboard', mcp: 'MCP', exports: 'Exports',
    'alert-silences': 'Silences', monitors: 'Cron monitors',
  } as Record<string, string>,
}

registerCatalogue('contextPanel', contextPanel)
