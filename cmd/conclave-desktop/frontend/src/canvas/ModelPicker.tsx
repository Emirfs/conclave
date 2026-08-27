import { memo, useEffect, useRef, useState } from 'react'

import { ProviderModels } from '../../wailsjs/go/main/App'
import { domain } from '../../wailsjs/go/models'
import { providerStyle } from '../providers'

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

/** Which model each of a card's providers runs on. One chip per provider, small
 *  enough to sit in the toolbar: a card is for the conversation, and the model
 *  is something you set once and then only want to be able to read off. */
export const ModelPicker = memo(function ModelPicker({ providers, chosen, onSave }: Props) {
  return (
    <span className="node__models">
      {providers.map((name) => (
        <ModelChip key={name} provider={name} model={chosen[name] ?? ''} onSave={onSave} />
      ))}
    </span>
  )
})

function ModelChip({
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
  const [open, setOpen] = useState(false)
  // A model the daemon does not list is still valid: a CLI gains models without
  // this build changing, so anything can be typed in.
  const [custom, setCustom] = useState(false)
  const [draft, setDraft] = useState(model)
  const chip = useRef<HTMLDivElement>(null)

  useEffect(() => setDraft(model), [model])

  // The list is only worth fetching once the user asks to see it.
  useEffect(() => {
    if (!open || known) return
    let live = true
    void models(provider).then((list) => {
      if (live) setKnown(list)
    })
    return () => {
      live = false
    }
  }, [open, known, provider])

  // A menu that stays open behind a click elsewhere on the board would cover
  // the card underneath it.
  useEffect(() => {
    if (!open) return
    const dismiss = (event: MouseEvent) => {
      if (!chip.current?.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', dismiss)
    return () => document.removeEventListener('mousedown', dismiss)
  }, [open])

  const listed = known?.models ?? []
  const fallback = known?.default ?? ''
  const options = model !== '' && !listed.includes(model) ? [model, ...listed] : listed

  const commit = (value: string) => {
    setOpen(false)
    setCustom(false)
    if (value !== model) void onSave(provider, value)
  }

  return (
    <div className="node__model" ref={chip}>
      <button
        className={`node__model-chip${open ? ' node__model-chip--open' : ''}`}
        style={{ ['--badge-accent' as string]: style.accent }}
        onClick={() => setOpen((current) => !current)}
        title={
          model === ''
            ? `${style.label} kendi varsayılanıyla çalışıyor`
            : `${style.label} bu kartta ${model} ile çalışıyor`
        }
      >
        <span className="node__model-glyph">{style.glyph}</span>
        <span className="node__model-name">{model || 'varsayılan'}</span>
      </button>

      {open && (
        <div className="node__model-menu">
          <button
            className={`node__model-option${model === '' ? ' node__model-option--active' : ''}`}
            onClick={() => commit('')}
          >
            {fallback ? `varsayılan (${fallback})` : 'varsayılan'}
          </button>
          {options.map((name) => (
            <button
              key={name}
              className={`node__model-option${name === model ? ' node__model-option--active' : ''}`}
              onClick={() => commit(name)}
            >
              {name}
            </button>
          ))}
          {known === null && <span className="node__model-empty">yükleniyor…</span>}
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
                  commit(draft.trim())
                }
                if (event.key === 'Escape') {
                  setCustom(false)
                  setDraft(model)
                }
              }}
              onBlur={() => commit(draft.trim())}
            />
          ) : (
            <button
              className="node__model-option node__model-option--custom"
              onClick={() => {
                setDraft(model)
                setCustom(true)
              }}
            >
              özel…
            </button>
          )}
        </div>
      )}
    </div>
  )
}
