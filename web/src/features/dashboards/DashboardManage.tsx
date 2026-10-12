import { useState, type FormEvent } from 'react'
import { PencilIcon, PlusIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { dashEdit } from '@/messages/dashEdit'

import { type TemplateKey } from './editor/model'

const t = dashEdit
const templateKeys: TemplateKey[] = ['standard', 'marketing', 'product', 'performance', 'blank']

type DashboardManageProps = {
  /** Owner, admin or editor. Viewers never see these controls (the API refuses them anyway). */
  canWrite: boolean
  /** The saved dashboard on screen, if any. */
  currentId?: number
  editing: boolean
  onEdit: (id: number) => void
  onNew: (template: TemplateKey) => void
}

/** Entry points to the editor: edit the dashboard on screen, or start a new one from a template. */
export function DashboardManage({ canWrite, currentId, editing, onEdit, onNew }: DashboardManageProps) {
  const [open, setOpen] = useState(false)
  const [template, setTemplate] = useState<TemplateKey>('standard')
  if (!canWrite || editing) return null

  const submit = (event: FormEvent) => {
    event.preventDefault()
    setOpen(false)
    onNew(template)
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
      {currentId !== undefined ? (
        <Button className="min-h-11 sm:min-h-9" type="button" variant="outline" onClick={() => onEdit(currentId)}>
          <PencilIcon />{t.edit}
        </Button>
      ) : null}
      <Button className="min-h-11 sm:min-h-9" type="button" variant="outline" onClick={() => setOpen(true)}>
        <PlusIcon />{t.newDashboard}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t.newTitle}</DialogTitle>
            <DialogDescription>{t.newBody}</DialogDescription>
          </DialogHeader>
          <form className="flex flex-col gap-4" onSubmit={submit}>
            <Field>
              <FieldLabel htmlFor="dash-new-template">{t.templateLabel}</FieldLabel>
              <Select value={template} onValueChange={(value) => setTemplate(value as TemplateKey)}>
                <SelectTrigger className="min-h-11 w-full sm:min-h-9" id="dash-new-template"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {templateKeys.map((key) => <SelectItem key={key} value={key}>{t.templates[key]}</SelectItem>)}
                  </SelectGroup>
                </SelectContent>
              </Select>
              <FieldDescription>{t.templateNotes[template]}</FieldDescription>
            </Field>
            <DialogFooter className="gap-2">
              <Button className="min-h-11 sm:min-h-9" type="button" variant="outline" onClick={() => setOpen(false)}>{t.cancel}</Button>
              <Button className="min-h-11 sm:min-h-9" type="submit">{t.start}</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
