import type { Edge } from '@xyflow/react'

import { LINK_MODES } from './useCanvas'

const ROUND_CHOICES = [1, 2, 3, 5, 8]

/** Options for the selected link: how the two cards work together and for how
 *  many rounds. Shown only while a link is selected. */
export function LinkPanel({
  edge,
  onConfigure,
  onUnlink,
}: {
  edge: Edge
  onConfigure: (id: string, mode: string, rounds: number) => void
  onUnlink: (id: string) => void
}) {
  const mode = (edge.data?.mode as string) ?? 'relay'
  const rounds = (edge.data?.maxRounds as number) ?? 3

  return (
    <div className="linkpanel">
      <span className="linkpanel__title">Bağlantı</span>
      <div className="linkpanel__row">
        {Object.entries(LINK_MODES).map(([value, label]) => (
          <button
            key={value}
            className={`linkpanel__choice${mode === value ? ' linkpanel__choice--active' : ''}`}
            onClick={() => onConfigure(edge.id, value, rounds)}
            title={describeMode(value)}
          >
            {label}
          </button>
        ))}
      </div>
      <div className="linkpanel__row">
        <span className="linkpanel__label">tur</span>
        {ROUND_CHOICES.map((value) => (
          <button
            key={value}
            className={`linkpanel__choice${rounds === value ? ' linkpanel__choice--active' : ''}`}
            onClick={() => onConfigure(edge.id, mode, value)}
          >
            {value}
          </button>
        ))}
      </div>
      <p className="linkpanel__hint">{describeMode(mode)}</p>
      <button className="linkpanel__remove" onClick={() => onUnlink(edge.id)}>
        Bağlantıyı kaldır
      </button>
    </div>
  )
}

function describeMode(mode: string): string {
  switch (mode) {
    case 'dialogue':
      return 'İki kart birbirine cevap verir. Ters yön otomatik kurulur; tur sayısı kadar sürer.'
    case 'review':
      return 'Hedef kart gelen çıktıyı inceler ve eksikleri söyler.'
    default:
      return 'Tek yönlü devir: kaynağın cevabı hedefe olduğu gibi geçer.'
  }
}
