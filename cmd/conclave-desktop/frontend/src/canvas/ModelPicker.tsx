import { memo, useEffect, useRef, useState } from 'react'

import { ProviderModels } from '../../wailsjs/go/main/App'
import { domain } from '../../wailsjs/go/models'
import { providerStyle } from '../providers'

/** How long a fetched list is reused before the provider is asked again. The
 *  daemon caches for longer; this is only what keeps opening the same menu
 *  twice in a row from crossing the wire, while a model pulled a minute ago
 *  still shows up without restarting the app. */
const CATALOG_TTL = 60_000

const catalog = new Map<string, { at: number; list: Promise<domain.ProviderModels> }>()

function models(name: string): Promise<domain.ProviderModels> {
  const entry = catalog.get(name)
  if (entry && Date.now() - entry.at < CATALOG_TTL) return entry.list
  const list = ProviderModels(name).catch(() =>
    domain.ProviderModels.createFrom({ provider: name, models: [], default: '' }),
  )
  catalog.set(name, { at: Date.now(), list })
  return list
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

  // The list is only worth fetching once the user asks to see it, and is asked
  // for again on a later open so a newly installed model appears.
  useEffect(() => {
    if (!open) return
    let live = true
    void models(provider).then((list) => {
      if (live) setKnown(list)
    })
    return () => {
      live = false
    }
  }, [open, provider])

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
  // A model chosen before the provider dropped it from its list still runs, so
  // it stays on the menu rather than disappearing from under the user.
  const options =
    model !== '' && !listed.some((item) => item.id === model)
      ? [domain.Model.createFrom({ id: model }), ...listed]
      : listed

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
          {options.map((item) => (
            <button
              key={item.id}
              className={`node__model-option${item.id === model ? ' node__model-option--active' : ''}`}
              onClick={() => commit(item.id)}
              title={item.id}
            >
              {/* The provider's own name for it, with the id underneath: the id
                  is what the CLI is given, and models differ only by suffix. */}
              <span className="node__model-label">{item.label || item.id}</span>
              {item.label && item.label !== item.id && (
                <span className="node__model-id">{item.id}</span>
              )}
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
