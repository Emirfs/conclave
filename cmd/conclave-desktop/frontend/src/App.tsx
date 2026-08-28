import { useCallback, useEffect, useRef, useState } from 'react'

import { EnsureDaemon, Snapshot } from '../wailsjs/go/main/App'
import { Quit, WindowMinimise, WindowToggleMaximise } from '../wailsjs/runtime/runtime'
import { domain } from '../wailsjs/go/models'
import { providerStyle } from './providers'
import { Board } from './canvas/Board'
import { useCanvas } from './canvas/useCanvas'
import { UpdateBanner } from './UpdateBanner'
import { UsagePanel } from './UsagePanel'
import './canvas/canvas.css'

type Connection = 'connecting' | 'online' | 'offline'

const POLL_INTERVAL = 1500

export function App() {
  const [snapshot, setSnapshot] = useState<domain.Snapshot | null>(null)
  const [connection, setConnection] = useState<Connection>('connecting')
  const [error, setError] = useState<string | null>(null)
  const [starting, setStarting] = useState(false)
  const [usageOpen, setUsageOpen] = useState(false)
  // Guards against a slow response from a previous poll overwriting a newer one.
  const generation = useRef(0)

  const poll = useCallback(async () => {
    const current = ++generation.current
    try {
      const next = await Snapshot()
      if (current !== generation.current) return
      setSnapshot(next)
      setConnection('online')
      setError(null)
    } catch (cause) {
      if (current !== generation.current) return
      setConnection('offline')
      setError(String(cause))
    }
  }, [])

  useEffect(() => {
    void poll()
    const timer = window.setInterval(() => void poll(), POLL_INTERVAL)
    return () => window.clearInterval(timer)
  }, [poll])

  const startDaemon = useCallback(async () => {
    setStarting(true)
    setError(null)
    setConnection('connecting')
    try {
      await EnsureDaemon()
      await poll()
    } catch (cause) {
      setConnection('offline')
      setError(String(cause))
    } finally {
      setStarting(false)
    }
  }, [poll])

  const providers = (snapshot?.providers ?? []).filter((item) => item.kind !== 'memory')
  const memory = (snapshot?.providers ?? []).filter((item) => item.kind === 'memory')
  const ready = providers.filter((item) => item.available).map((item) => item.name)

  const canvas = useCanvas(connection === 'online')

  const addSolo = useCallback(
    (name: string) => {
      const style = providerStyle(name)
      void canvas.addConversation({
        title: style.label,
        kind: 'solo',
        providers: [name],
        ...scatter(),
      } as domain.NewConversation)
    },
    [canvas],
  )

  const addNote = useCallback(() => {
    void canvas.addNote({ body: '', color: '', ...scatter() } as domain.NewNote)
  }, [canvas])

  // A pipeline card holds deterministic work: an ordered command list run in a
  // project. It has no provider, so it is created from the rail, not by
  // clicking one.
  const addPipeline = useCallback(() => {
    void canvas.addPipeline({ title: 'Pipeline', ...scatter() } as domain.NewPipeline)
  }, [canvas])

  // A join is a waiting point: every line feeding it must speak before it
  // passes anything on, and then it passes on one message carrying all of them.
  const addJoin = useCallback(() => {
    void canvas.addJoin({ body: 'Birleştirici', color: '', ...scatter() } as domain.NewNote)
  }, [canvas])

  const addGroup = useCallback(() => {
    if (ready.length === 0) return
    void canvas.addConversation({
      title: 'Hepsi birden',
      kind: 'group',
      providers: ready.slice(0, 4),
      ...scatter(),
    } as domain.NewConversation)
  }, [canvas, ready])

  return (
    <div className="shell">
      <TitleBar connection={connection} version={snapshot?.version} />
      <UpdateBanner online={connection === 'online'} />
      <div className="body">
        <aside className="rail">
          <ProviderGroup heading="Sağlayıcılar" providers={providers} onPick={addSolo} />
          <section className="rail__group">
            <h2 className="rail__heading">Panoya ekle</h2>
            <button className="button button--block" onClick={addGroup} disabled={ready.length === 0}>
              Grup konuşması
            </button>
            <button className="button button--block" onClick={addNote}>
              Not
            </button>
            <button className="button button--block" onClick={addPipeline}>
              Pipeline
            </button>
            <button className="button button--block" onClick={addJoin}>
              Birleştirici
            </button>
          </section>
          <section className="rail__group">
            <h2 className="rail__heading">Pano</h2>
            <button className="button button--block" onClick={() => void canvas.exportBoard()}>
              Dışa aktar
            </button>
            <button className="button button--block" onClick={() => void canvas.importBoard()}>
              İçe aktar
            </button>
            <button className="button button--block" onClick={() => setUsageOpen((open) => !open)}>
              {usageOpen ? 'Kullanımı gizle' : 'Kullanım'}
            </button>
            <p className="rail__hint">
              İçe aktarma hiçbir şeyin yerine geçmez: dosyadaki kartlar panoda
              duranların yanına eklenir.
            </p>
            <p className="rail__hint">
              Sağlayıcıya tıkla: tekil konuşma. Boş zemine çift tıkla: not.
              Boş zeminde sürükle: toplu seç. Ctrl+A: hepsi. Orta/sağ tuş: panoyu kaydır.
              İki kart seç, bağlama düğmeleri çıksın.
            </p>
          </section>
          {usageOpen && (
            <UsagePanel online={connection === 'online'} onClose={() => setUsageOpen(false)} />
          )}
          {memory.length > 0 && <ProviderGroup heading="Bellek" providers={memory} />}
        </aside>
        <main className={connection === 'online' ? 'stage stage--board' : 'stage'}>
          {connection === 'online' ? (
            <Board canvas={canvas} providers={ready} />
          ) : (
            <Stage
              connection={connection}
              error={error}
              starting={starting}
              onStart={startDaemon}
            />
          )}
        </main>
      </div>
    </div>
  )
}

function TitleBar({ connection, version }: { connection: Connection; version?: string }) {
  const label =
    connection === 'online'
      ? `daemon ${version ?? ''}`.trim()
      : connection === 'connecting'
        ? 'bağlanıyor'
        : 'daemon kapalı'

  return (
    <header className="titlebar">
      <span className="titlebar__mark">Conclave</span>
      <span className={`pill pill--${connection}`}>
        <span className="pill__dot" />
        {label}
      </span>
      <span className="titlebar__spacer" />
      <div className="titlebar__controls">
        <button className="titlebar__button" onClick={WindowMinimise} title="Küçült">
          <CaptionIcon d="M2 8h12" />
        </button>
        <button className="titlebar__button" onClick={WindowToggleMaximise} title="Büyüt">
          <CaptionIcon d="M3.5 3.5h9v9h-9z" />
        </button>
        <button
          className="titlebar__button titlebar__button--close"
          onClick={Quit}
          title="Kapat"
        >
          <CaptionIcon d="M3.5 3.5l9 9M12.5 3.5l-9 9" />
        </button>
      </div>
    </header>
  )
}

/** Offsets each new node a little so a burst of additions does not stack into
 *  one pile at the origin. */
function scatter() {
  return { x: 80 + Math.random() * 320, y: 60 + Math.random() * 220 }
}

function CaptionIcon({ d }: { d: string }) {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
      <path d={d} fill="none" stroke="currentColor" strokeWidth="1.1" />
    </svg>
  )
}

function ProviderGroup({
  heading,
  providers,
  onPick,
}: {
  heading: string
  providers: domain.Provider[]
  onPick?: (name: string) => void
}) {
  return (
    <section className="rail__group">
      <h2 className="rail__heading">{heading}</h2>
      {providers.length === 0 ? (
        <p className="provider__kind" style={{ padding: '0 8px' }}>
          Henüz keşfedilmedi
        </p>
      ) : (
        providers.map((item) => (
          <ProviderRow key={item.name} provider={item} onPick={onPick} />
        ))
      )}
    </section>
  )
}

function ProviderRow({
  provider,
  onPick,
}: {
  provider: domain.Provider
  onPick?: (name: string) => void
}) {
  const style = providerStyle(provider.name)
  const actionable = Boolean(onPick) && provider.available
  return (
    <button
      type="button"
      className={`provider provider--${provider.available ? 'online' : 'offline'}${
        actionable ? ' provider--actionable' : ''
      }`}
      title={actionable ? `${style.label} ile konuşma aç` : (provider.command ?? 'bulunamadı')}
      onClick={actionable ? () => onPick?.(provider.name) : undefined}
      disabled={!actionable}
    >
      <span
        className="provider__badge"
        style={{ ['--badge-accent' as string]: style.accent }}
      >
        {style.glyph}
      </span>
      <span>
        <span className="provider__name">{style.label}</span>
        <br />
        <span className="provider__kind">{provider.kind}</span>
      </span>
      <span className="provider__state">{provider.available ? 'hazır' : 'yok'}</span>
      {provider.quota && (
        <div className="provider__quota" style={{ ['--badge-accent' as string]: style.accent }}>
          <QuotaBar
            label={provider.quota.short_label}
            utilization={provider.quota.short_utilization}
            resetsAt={provider.quota.short_resets_at}
          />
          <QuotaBar
            label={provider.quota.long_label}
            utilization={provider.quota.long_utilization}
            resetsAt={provider.quota.long_resets_at}
          />
        </div>
      )}
    </button>
  )
}

/** Shows how much of a window is spent, using the provider's own numbers. */
function QuotaBar({
  label,
  utilization,
  resetsAt,
}: {
  label?: string
  utilization: number
  resetsAt?: number
}) {
  if (!label) return null
  const percent = Math.min(100, Math.max(0, Math.round(utilization * 100)))
  return (
    <div className="quota" title={`${label}: %${percent} kullanıldı${resetsIn(resetsAt)}`}>
      <span className="quota__label">{label}</span>
      <span className="quota__track">
        <span className="quota__fill" style={{ width: `${percent}%` }} />
      </span>
      <span className="quota__value">%{percent}</span>
    </div>
  )
}

/** Reset times arrive as Unix seconds; zero means the provider did not say. */
function resetsIn(resetsAt?: number): string {
  if (!resetsAt) return ''
  const remaining = resetsAt * 1000 - Date.now()
  if (remaining <= 0) return ', yenilendi'
  const hours = Math.floor(remaining / 3_600_000)
  const minutes = Math.floor((remaining % 3_600_000) / 60_000)
  return hours > 0 ? `, ${hours}s ${minutes}d sonra yenilenir` : `, ${minutes}d sonra yenilenir`
}

function Stage({
  connection,
  error,
  starting,
  onStart,
}: {
  connection: Connection
  error: string | null
  starting: boolean
  onStart: () => void
}) {
  if (connection === 'online') {
    return (
      <div className="stage__inner">
        <p className="stage__title">Zemin hazır</p>
        <p className="stage__hint">
          Sonsuz canvas bir sonraki aşamada buraya gelecek: sürüklenebilir konuşma
          node&apos;ları ve sticky note&apos;lar.
        </p>
      </div>
    )
  }

  return (
    <div className="stage__inner">
      <p className="stage__title">
        {connection === 'connecting' ? 'Daemon aranıyor' : 'Daemon çalışmıyor'}
      </p>
      <p className="stage__hint">
        Conclave durumu daemon&apos;da tutar. Arayüz yalnızca ona bağlanan bir istemci.
      </p>
      {error && <p className="stage__error">{error}</p>}
      <button className="button" onClick={onStart} disabled={starting}>
        {starting ? 'Başlatılıyor…' : 'Daemon’u başlat'}
      </button>
    </div>
  )
}
