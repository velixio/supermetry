import { useState } from 'react'
import { KeyRoundIcon } from 'lucide-react'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/button'
import { uiSettings } from '@/messages/uiSettings'

import { CreateKeyWizard } from './CreateKeyWizard'
import { McpClientConfig } from './McpClientConfig'

export function McpPage({ orgId, role, sites }: { orgId: number; role: string; sites: { id: number; name: string }[] }) {
  const t = uiSettings.mcp
  const [open, setOpen] = useState(false)
  const canCreate = role === 'owner' || role === 'admin'
  return (
    <PageContainer className="max-w-[1200px]">
      <PageHeader
        title={t.title}
        description={t.description}
        actions={canCreate ? (
          <Button type="button" onClick={() => setOpen(true)}>
            <KeyRoundIcon data-icon="inline-start" />
            {t.createForThis}
          </Button>
        ) : null}
      />
      <p className="text-sm text-muted-foreground">{t.placeholderNote}</p>
      <McpClientConfig apiKey="<key>" />
      {canCreate ? <CreateKeyWizard open={open} onOpenChange={setOpen} orgId={orgId} sites={sites} initialPurpose="mcp" /> : null}
    </PageContainer>
  )
}
