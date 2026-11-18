import { useState, type FormEvent } from 'react'
import { PencilIcon } from 'lucide-react'

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
  /** The new-dashboard dialog is opened from the dashboard switcher, so its state lives with the page. */
  newOpen: boolean
  onNewOpenChange: (open: boolean) => void
}

/** Entry points to the editor: edit the dashboard on screen; the new-from-template dialog opens from the switcher. */
export function DashboardManage({ canWrite, currentId, editing, onEdit, onNew, newOpen: open, onNewOpenChange: setOpen }: DashboardManageProps) {
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
        <Button className="max-sm:min-h-11" size="sm" type="button" variant="ghost" onClick={() => onEdit(currentId)}>
          <PencilIcon />{t.edit}
        </Button>
      ) : null}
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
                <SelectTrigger className="max-sm:min-h-11 w-full sm:min-h-9" id="dash-new-template"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {templateKeys.map((key) => <SelectItem key={key} value={key}>{t.templates[key]}</SelectItem>)}
                  </SelectGroup>
                </SelectContent>
              </Select>
              <FieldDescription>{t.templateNotes[template]}</FieldDescription>
            </Field>
            <DialogFooter className="gap-2">
              <Button className="max-sm:min-h-11" type="button" variant="outline" onClick={() => setOpen(false)}>{t.cancel}</Button>
              <Button className="max-sm:min-h-11" type="submit">{t.start}</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
