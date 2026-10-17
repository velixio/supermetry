import { describe, expect, it } from 'vitest'

import { compareCatalogue, type MessageTree } from '@/lib/i18n/catalogue'
import { en } from '@/messages/en'
import { dashEdit } from '@/messages/dashEdit'
import { ui } from '@/messages/ui'
import { uiAccount } from '@/messages/uiAccount'
import { uiFlows } from '@/messages/uiFlows'
import de from './de'
import deDashEdit from './de.dashEdit'
import es from './es'
import esDashEdit from './es.dashEdit'
import deUi from './de.ui'
import deUiAccount from './de.uiAccount'
import esUi from './es.ui'
import esUiAccount from './es.uiAccount'
import bn from './bn'
import bnDashEdit from './bn.dashEdit'
import bnUi from './bn.ui'
import bnUiAccount from './bn.uiAccount'
import esUiFlows from './es.uiFlows'
import deUiFlows from './de.uiFlows'
import bnUiFlows from './bn.uiFlows'

const english = en as unknown as MessageTree
const englishDashEdit = dashEdit as unknown as MessageTree
const englishUi = ui as unknown as MessageTree
const englishUiAccount = uiAccount as unknown as MessageTree
const englishUiFlows = uiFlows as unknown as MessageTree

describe.each([
  ['es', es, esDashEdit, esUi, esUiAccount, esUiFlows],
  ['de', de, deDashEdit, deUi, deUiAccount, deUiFlows],
  ['bn', bn, bnDashEdit, bnUi, bnUiAccount, bnUiFlows],
] as const)('%s catalogue', (_code, main, editor, uiTree, accountTree, flowsTree) => {
  it('has every English key, no extras, and matching placeholders and function arguments', () => {
    expect(compareCatalogue(english, main)).toEqual([])
  })

  it('covers the dashboard editor catalogue the same way', () => {
    expect(compareCatalogue(englishDashEdit, editor)).toEqual([])
  })

  it('covers the ui catalogue the same way', () => {
    expect(compareCatalogue(englishUi, uiTree)).toEqual([])
    expect(compareCatalogue(englishUiAccount, accountTree)).toEqual([])
    expect(compareCatalogue(englishUiFlows, flowsTree)).toEqual([])
  })

  it('translates rather than copying English for most strings', () => {
    const flat = (tree: MessageTree, other: MessageTree): [number, number] => {
      let total = 0
      let same = 0
      for (const [key, value] of Object.entries(tree)) {
        const counterpart = other[key]
        if (typeof value === 'string') {
          total += 1
          if (counterpart === value) same += 1
        } else if (typeof value === 'object' && value !== null && typeof counterpart === 'object' && counterpart !== null) {
          const [t, s] = flat(value as MessageTree, counterpart as MessageTree)
          total += t
          same += s
        }
      }
      return [total, same]
    }
    const [total, same] = flat(english, main)
    // Brand names, units and codes legitimately stay identical; a copied catalogue does not.
    expect(same / total).toBeLessThan(0.25)
  })
})
