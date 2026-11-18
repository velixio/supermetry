import type { MessageTree } from '@/lib/i18n/catalogue'

// ES translation of messages/contextPanel.ts. Machine-assisted first translation (reviewed: false in the manifest).
const messages: MessageTree = {
  label: 'Contexto de la página',
  shortcutName: 'Contexto de la página',
  sections: { quick: 'Acceso rápido', related: 'Relacionado', recommended: 'Sugerido', recent: 'Reciente' },
  savedDashboard: (name: string) => `Panel: ${name}`,
  savedSegment: (name: string) => `Segmento: ${name}`,
  recommend: {
    install: 'Instalar el rastreador',
    createGoal: 'Crear un objetivo',
    recordingOff: 'Activar la grabación',
    rejected: 'Revisar eventos rechazados',
  },
  labels: {
    dashboard: 'Panel', mcp: 'MCP', exports: 'Exportaciones',
    'alert-silences': 'Silencios', monitors: 'Monitores cron',
  },
}

export default messages
