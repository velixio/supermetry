import { useState } from 'react'

import { SegmentedControl } from '@/components/SegmentedControl'
import { uiSettings } from '@/messages/uiSettings'

import { CopyBlock } from './CopyBlock'
import { mcpConfig } from './presets'

type Client = 'claude' | 'cursor' | 'generic'

/** Client switcher plus the config snippet. `apiKey` is the real secret, or the "<key>" placeholder. */
export function McpClientConfig({ apiKey }: { apiKey: string }) {
  const t = uiSettings.mcp
  const [client, setClient] = useState<Client>('claude')
  const options = (Object.keys(t.clients) as Client[]).map((value) => ({ value, label: t.clients[value] }))
  return (
    <div className="flex flex-col gap-3">
      <SegmentedControl aria-label={t.clientLabel} value={client} onValueChange={setClient} options={options} panelId="mcp-config-panel" />
      <div id="mcp-config-panel" role="tabpanel" className="flex flex-col gap-2">
        <p className="text-sm text-muted-foreground">{t.intro[client]}</p>
        <CopyBlock value={mcpConfig(apiKey)} what={`${t.clients[client]} configuration`} />
      </div>
    </div>
  )
}
