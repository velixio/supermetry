import type { MessageTree } from '@/lib/i18n/catalogue'

// DE translation of messages/contextPanel.ts. Machine-assisted first translation (reviewed: false in the manifest).
const messages: MessageTree = {
  label: 'Seitenkontext',
  shortcutName: 'Seitenkontext',
  sections: { quick: 'Schnellzugriff', related: 'Verwandt', recommended: 'Vorschläge', recent: 'Zuletzt' },
  savedDashboard: (name: string) => `Dashboard: ${name}`,
  savedSegment: (name: string) => `Segment: ${name}`,
  recommend: {
    install: 'Tracker installieren',
    createGoal: 'Ziel anlegen',
    recordingOff: 'Aufzeichnung einschalten',
    rejected: 'Abgelehnte Ereignisse prüfen',
  },
  labels: {
    dashboard: 'Dashboard', mcp: 'MCP', exports: 'Exporte',
    'alert-silences': 'Stummschaltungen', monitors: 'Cron-Monitore',
  },
}

export default messages
