import { useState } from 'react'

import { providerStyle } from '../providers'

/** Picks which providers an answer should be carried into.
 *
 *  Several at once is the interesting case: the same answer taken in different
 *  directions is what makes a board worth having, rather than one card that
 *  eventually agrees with itself.
 */
export function BranchPanel({
  providers,
  answer,
  onBranch,
  onCancel,
}: {
  providers: string[]
  answer: string
  onBranch: (providers: string[]) => void
  onCancel: () => void
}) {
  const [chosen, setChosen] = useState<string[]>([])

  const toggle = (name: string) =>
    setChosen((current) =>
      current.includes(name) ? current.filter((item) => item !== name) : [...current, name],
    )

  return (
    <div className="branchpanel">
      <span className="branchpanel__title">Bu cevaptan dallan</span>
      <p className="branchpanel__excerpt">{excerpt(answer)}</p>
      <div className="branchpanel__row">
        {providers.map((name) => {
          const style = providerStyle(name)
          return (
            <button
              key={name}
              className={`branchpanel__choice${chosen.includes(name) ? ' branchpanel__choice--active' : ''}`}
              style={{ ['--branch-accent' as string]: style.accent }}
              onClick={() => toggle(name)}
            >
              {style.label}
            </button>
          )
        })}
      </div>
      <p className="branchpanel__hint">
        Seçilen her sağlayıcı için ayrı bir kart açılır ve bu cevapla başlar.
        Kartlar birbirinden bağımsız ilerler.
      </p>
      <div className="branchpanel__row">
        <button
          className="branchpanel__go"
          onClick={() => onBranch(chosen)}
          disabled={chosen.length === 0}
        >
          {chosen.length > 1 ? `${chosen.length} kart aç` : 'kart aç'}
        </button>
        <button className="branchpanel__cancel" onClick={onCancel}>
          vazgeç
        </button>
      </div>
    </div>
  )
}

function excerpt(answer: string): string {
  const flat = answer.replace(/\s+/g, ' ').trim()
  return flat.length > 160 ? `${flat.slice(0, 160)}…` : flat
}
