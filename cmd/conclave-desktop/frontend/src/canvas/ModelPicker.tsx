import { memo, useEffect, useState } from 'react'

import { ProviderModels } from '../../wailsjs/go/main/App'
import { domain } from '../../wailsjs/go/models'
import { providerStyle } from '../providers'

/** The select value that leaves the card on the provider's own default, and the
 *  one that swaps the select for a free text field. Neither can collide with a
 *  model name: one is empty, the other holds a space. */
const DEFAULT = ''
const CUSTOM = ' custom'

/** One request per provider per session. The lists barely change, and opening a
 *  card must not cost a process launch for every other card on the board. */
const catalog = new Map<string, Promise<domain.ProviderModels>>()

function models(name: string): Promise<domain.ProviderModels> {
  let pending = catalog.get(name)
  if (!pending) {
    pending = ProviderModels(name).catch(
      () => ({ provider: name, models: [], default: '' }) as domain.ProviderModels,
    )
    catalog.set(name, pending)
  }
  return pending
}

type Props = {
  providers: string[]
  /** Chosen model per provider; a provider missing from it runs on the CLI default. */
  chosen: Record<string, string>
  onSave: (provider: string, model: string) => Promise<void>
}

/** Which model each of a card's providers runs on. A group card lists one row
 *  per provider, because the whole point of it is that they differ. */
export const ModelPicker = memo(function ModelPicker({ providers, chosen, onSave }: Props) {
  return (
    <div className="node__models nodrag">
      {providers.map((name) => (
        <ModelRow key={name} provider={name} model={chosen[name] ?? ''} onSave={onSave} />
      ))}
    </div>
  )
})

function ModelRow({
  provider,
  model,
  onSave,
}: {
  provider: string
  model: string
  onSave: (provider: string, model: string) => Promise<void>
}) {
  const style = providerStyle(provider)
  const [known, setKnown] = useState<domain.ProviderModels | null>(null)
  // A model the daemon does not list is still valid: a CLI gains models without
  // this build changing, so the field stays open for anything.
  const [custom, setCustom] = useState(false)
  const [draft, setDraft] = useState(model)

  useEffect(() => setDraft(model), [model])
  useEffect(() => {
    let live = true
    void models(provider).then((list) => {
      if (live) setKnown(list)
    })
    return () => {
      live = false
    }
  }, [provider])

  const listed = known?.models ?? []
  const fallback = known?.default ?? ''
  const options = model !== '' && !listed.includes(model) ? [model, ...listed] : listed

  const commit = (value: string) => {
    if (value !== model) void onSave(provider, value)
  }

  return (
    <div className="node__model">
      <span
        className="node__badge"
        style={{ ['--badge-accent' as string]: style.accent }}
        title={style.label}
      >
        {style.glyph}
      </span>
      {custom ? (
        <input
          className="node__model-input"
          value={draft}
          autoFocus
          placeholder="model adı"
          spellCheck={false}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={(event) => {
            event.stopPropagation()
            if (event.key === 'Enter') {
              event.preventDefault()
              setCustom(false)
              commit(draft.trim())
            }
            if (event.key === 'Escape') {
              setCustom(false)
              setDraft(model)
            }
          }}
          onBlur={() => {
            setCustom(false)
            commit(draft.trim())
          }}
        />
      ) : (
        <select
          className="node__model-select"
          value={model === '' ? DEFAULT : model}
          onChange={(event) => {
            if (event.target.value === CUSTOM) {
              setDraft(model)
              setCustom(true)
              return
            }
            commit(event.target.value)
          }}
          title={
            model === ''
              ? `${style.label} kendi varsayılanıyla çalışıyor`
              : `${style.label} bu kartta ${model} ile çalışıyor`
          }
        >
          <option value={DEFAULT}>{fallback ? `varsayılan (${fallback})` : 'varsayılan'}</option>
          {options.map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
          <option value={CUSTOM}>özel…</option>
        </select>
      )}
    </div>
  )
}
