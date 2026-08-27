import { useEffect, useState } from 'react'
import type { Edge } from '@xyflow/react'

import { ROLE_PAIRS } from './roles'
import { LINK_MODES } from './useCanvas'

const ROUND_CHOICES = [1, 2, 3, 5, 8]

/** Options for the selected link: how the two cards work together, for how many
 *  rounds, what they are both working on, and which side does what. Shown only
 *  while a link is selected. */
export function LinkPanel({
  edge,
  onConfigure,
  onAssignRoles,
  onUnlink,
}: {
  edge: Edge
  onConfigure: (id: string, mode: string, rounds: number, untilDone: boolean, briefing: string) => void
  onAssignRoles: (sourceNodeID: string, targetNodeID: string, sourceRole: string, targetRole: string) => void
  onUnlink: (id: string) => void
}) {
  const mode = (edge.data?.mode as string) ?? 'relay'
  const rounds = (edge.data?.maxRounds as number) ?? 3
  const untilDone = (edge.data?.untilDone as boolean) ?? false
  const briefing = (edge.data?.briefing as string) ?? ''
  // A line to a result card records where the result came from. There is no
  // exchange along it to configure — only the option to take it off the board.
  const toNote = (edge.data?.toNote as boolean) ?? false

  // The textarea is edited locally and committed on blur, so a refetch
  // mid-sentence cannot overwrite what is being typed.
  const [draft, setDraft] = useState(briefing)
  useEffect(() => setDraft(briefing), [briefing, edge.id])

  const commit = (next: Partial<{ mode: string; rounds: number; untilDone: boolean; briefing: string }>) =>
    onConfigure(
      edge.id,
      next.mode ?? mode,
      next.rounds ?? rounds,
      next.untilDone ?? untilDone,
      next.briefing ?? draft,
    )

  if (toNote) {
    return (
      <div className="linkpanel">
        <span className="linkpanel__title">Sonuç bağlantısı</span>
        <p className="linkpanel__hint">
          Bu çizgi, sonuç kartının hangi kartlardan çıktığını gösterir. Üzerinden
          mesaj geçmez.
        </p>
        <button className="linkpanel__remove" onClick={() => onUnlink(edge.id)}>
          Bağlantıyı kaldır
        </button>
      </div>
    )
  }

  return (
    <div className="linkpanel">
      <span className="linkpanel__title">Bağlantı</span>
      <div className="linkpanel__row">
        {Object.entries(LINK_MODES).map(([value, label]) => (
          <button
            key={value}
            className={`linkpanel__choice${mode === value ? ' linkpanel__choice--active' : ''}`}
            onClick={() => commit({ mode: value, untilDone: value === 'dialogue' && untilDone })}
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
            onClick={() => commit({ rounds: value, untilDone: false })}
          >
            {value}
          </button>
        ))}
      </div>
      <button
        className={`linkpanel__until${untilDone ? ' linkpanel__until--active' : ''}`}
        onClick={() => commit({ mode: 'dialogue', untilDone: !untilDone })}
        title="Testler geçene, sağlayıcı işi tamamlayana veya kullanıcı kararı gerekene kadar sürdür"
      >
        {untilDone ? 'bitene kadar açık' : 'iş bitene kadar sürdür'}
      </button>
      {mode !== 'relay' && (
        <div className="linkpanel__field">
          {/* A role only means something next to the other card's role, which is
              why the pair is chosen here rather than on each card. */}
          <span className="linkpanel__label">rol çifti</span>
          <div className="linkpanel__pairs">
            {ROLE_PAIRS.map((pair) => (
              <button
                key={pair.label}
                className="linkpanel__pair"
                onClick={() => onAssignRoles(edge.source, edge.target, pair.source.text, pair.target.text)}
                title={`${edge.source === edge.target ? '' : 'Kaynak kart: '}${pair.source.text}\n\nHedef kart: ${pair.target.text}`}
              >
                {pair.label}
              </button>
            ))}
          </div>
        </div>
      )}
      {mode !== 'relay' && (
        <label className="linkpanel__field">
          <span className="linkpanel__label">görev</span>
          <textarea
            className="linkpanel__briefing"
            value={draft}
            placeholder="İki kart ne üzerinde çalışıyor? Bir kez, ilk mesajdan önce iletilir."
            onChange={(event) => setDraft(event.target.value)}
            onBlur={() => draft !== briefing && commit({ briefing: draft })}
          />
        </label>
      )}
      <p className="linkpanel__hint">{describeMode(mode)}</p>
      {mode !== 'relay' && (
        <p className="linkpanel__hint">
          Görev metni her tura değil, her karta bir kez gider; kartlar bundan sonra
          birbirinin çıktısını doğrudan alır.
        </p>
      )}
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
