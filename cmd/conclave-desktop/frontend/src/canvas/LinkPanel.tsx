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
  onConfigure: (id: string, mode: string, rounds: number, untilDone: boolean) => void
  onUnlink: (id: string) => void
}) {
  const mode = (edge.data?.mode as string) ?? 'relay'
  const rounds = (edge.data?.maxRounds as number) ?? 3
  const untilDone = (edge.data?.untilDone as boolean) ?? false

  return (
    <div className="linkpanel">
      <span className="linkpanel__title">Bağlantı</span>
      <div className="linkpanel__row">
        {Object.entries(LINK_MODES).map(([value, label]) => (
          <button
            key={value}
            className={`linkpanel__choice${mode === value ? ' linkpanel__choice--active' : ''}`}
            onClick={() => onConfigure(edge.id, value, rounds, value === 'dialogue' && untilDone)}
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
            onClick={() => onConfigure(edge.id, mode, value, false)}
          >
            {value}
          </button>
        ))}
      </div>
      <button
        className={`linkpanel__until${untilDone ? ' linkpanel__until--active' : ''}`}
        onClick={() => onConfigure(edge.id, 'dialogue', rounds, !untilDone)}
        title="Testler geçene, sağlayıcı işi tamamlayana veya kullanıcı kararı gerekene kadar sürdür"
      >
        {untilDone ? 'bitene kadar açık' : 'iş bitene kadar sürdür'}
      </button>
      <p className="linkpanel__hint">{describeMode(mode)}</p>
      {untilDone && (
        <p className="linkpanel__hint">
          Kartların döngü sekmesinde “geçene kadar” testlerini başlat. Testler geçince veya kart tamam/kullanıcı girdisi işareti verince konuşma durur.
        </p>
      )}
      <button className="linkpanel__remove" onClick={() => onUnlink(edge.id)}>
        Bağlantıyı kaldır
      </button>
    </div>
  )
}

function describeMode(mode: string): string {
  switch (mode) {
    case 'dialogue':
      return 'İki kart birbirine cevap verir. Tur sınırı veya iş bitene kadar modu kullanılabilir.'
    case 'review':
      return 'Hedef kart gelen çıktıyı inceler ve eksikleri söyler.'
    default:
      return 'Tek yönlü devir: kaynağın cevabı hedefe olduğu gibi geçer.'
  }
}
