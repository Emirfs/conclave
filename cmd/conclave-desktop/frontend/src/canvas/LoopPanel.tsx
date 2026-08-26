import { useEffect, useState } from 'react'

import { domain } from '../../wailsjs/go/models'

const MODES: { value: string; label: string; hint: string }[] = [
  { value: 'off', label: 'kapalı', hint: 'Adımlar çalışmaz.' },
  {
    value: 'until_pass',
    label: 'geçene kadar',
    hint: 'Tüm adımlar başarılı olunca döngü kendiliğinden durur.',
  },
  {
    value: 'continuous',
    label: 'sürekli',
    hint: 'Başarılı da olsa durmaz; aralıkla tekrar eder. Donanım çevrimi için budur.',
  },
]

const INTERVALS = [0, 1, 5, 15, 60]

/** A card's step cycle: an ordered list of commands run in its project.
 *  Anything on the machine can be a step — a flasher, a serial listener, a
 *  test runner — because commands are executed directly, not through a shell. */
export function LoopPanel({
  conversationID,
  loop,
  running,
  runs,
  onSave,
  onToggleRunning,
}: {
  conversationID: number
  loop: domain.LoopConfig
  running: boolean
  runs: domain.CardRun[]
  onSave: (conversationID: number, config: domain.LoopConfig) => Promise<void>
  onToggleRunning: (conversationID: number, running: boolean) => Promise<void>
}) {
  const [steps, setSteps] = useState<domain.CardStep[]>(loop.steps ?? [])
  const [mode, setMode] = useState(loop.mode || 'off')
  const [interval, setIntervalSeconds] = useState(loop.interval_seconds ?? 5)
  const [notify, setNotify] = useState(loop.notify_on_failure ?? true)

  // Adopt server state when the card is reloaded, so a save elsewhere shows up.
  useEffect(() => {
    setSteps(loop.steps ?? [])
    setMode(loop.mode || 'off')
    setIntervalSeconds(loop.interval_seconds ?? 5)
    setNotify(loop.notify_on_failure ?? true)
  }, [loop])

  const update = (index: number, patch: Partial<domain.CardStep>) =>
    setSteps((current) =>
      current.map((step, at) => (at === index ? { ...step, ...patch } : step)),
    )

  const save = () =>
    void onSave(conversationID, {
      mode,
      interval_seconds: interval,
      notify_on_failure: notify,
      steps: steps.filter((step) => step.command.trim() !== ''),
    } as domain.LoopConfig)

  const active = MODES.find((item) => item.value === mode)

  return (
    <div className="loop">
      <div className="loop__steps">
        {steps.length === 0 && <p className="loop__empty">Henüz adım yok.</p>}
        {steps.map((step, index) => (
          <div className="loop__step" key={index}>
            <span className="loop__index">{index + 1}</span>
            <input
              className="loop__name nodrag"
              value={step.name}
              onChange={(event) => update(index, { name: event.target.value })}
              onKeyDown={(event) => event.stopPropagation()}
              placeholder="ad"
              spellCheck={false}
            />
            <input
              className="loop__command nodrag"
              value={step.command}
              onChange={(event) => update(index, { command: event.target.value })}
              onKeyDown={(event) => event.stopPropagation()}
              placeholder="STM32_Programmer_CLI -c port=SWD -w fw.hex -rst"
              spellCheck={false}
            />
            <input
              className="loop__timeout nodrag"
              type="number"
              min={0}
              value={step.timeout_seconds ?? 0}
              onChange={(event) =>
                update(index, { timeout_seconds: Number(event.target.value) })
              }
              onKeyDown={(event) => event.stopPropagation()}
              title="Saniye cinsinden zaman aşımı. Kendiliğinden bitmeyen bir dinleyici için gerekli. 0 = varsayılan."
            />
            <button
              className="loop__drop"
              onClick={() => setSteps((current) => current.filter((_, at) => at !== index))}
              title="Adımı sil"
            >
              ✕
            </button>
          </div>
        ))}
        <button
          className="loop__add"
          onClick={() =>
            setSteps((current) => [...current, { name: '', command: '', timeout_seconds: 0 }])
          }
        >
          Adım ekle
        </button>
      </div>

      <div className="loop__row">
        {MODES.map((item) => (
          <button
            key={item.value}
            className={`loop__choice${mode === item.value ? ' loop__choice--active' : ''}`}
            onClick={() => setMode(item.value)}
            title={item.hint}
          >
            {item.label}
          </button>
        ))}
      </div>

      <div className="loop__row">
        <span className="loop__label">aralık</span>
        {INTERVALS.map((value) => (
          <button
            key={value}
            className={`loop__choice${interval === value ? ' loop__choice--active' : ''}`}
            onClick={() => setIntervalSeconds(value)}
          >
            {value === 0 ? 'yok' : `${value}s`}
          </button>
        ))}
      </div>

      <label className="loop__toggle">
        <input
          type="checkbox"
          checked={notify}
          onChange={(event) => setNotify(event.target.checked)}
        />
        Hata olursa karta bildir
      </label>

      <p className="loop__hint">{active?.hint}</p>

      <div className="loop__row">
        <button className="loop__save" onClick={save}>
          Kaydet
        </button>
        <button
          className={running ? 'loop__stop' : 'loop__start'}
          onClick={() => void onToggleRunning(conversationID, !running)}
          disabled={mode === 'off' || steps.length === 0}
        >
          {running ? 'Durdur' : 'Başlat'}
        </button>
        {running && <span className="loop__live">çalışıyor</span>}
      </div>

      {runs.length > 0 && (
        <div className="loop__runs">
          <span className="loop__label">son çevrimler</span>
          {runs.map((run) => (
            <div key={run.id} className={`loop__run loop__run--${run.status}`}>
              <span className="loop__run-status">
                {run.status === 'passed' ? 'geçti' : run.status === 'failed' ? 'kaldı' : run.status}
              </span>
              <span className="loop__run-step">{run.step_name || 'tüm adımlar'}</span>
              {run.status === 'failed' && <span className="loop__run-code">exit {run.exit_code}</span>}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
