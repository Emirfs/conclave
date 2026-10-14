import { useCallback, useEffect, useRef, useState } from 'react'
import {
  AddGrantProgram, ApproveGrantResearch, ApproveGrantSend, CreateGrantProject,
  ExportGrantDossiers, ExportGrantResearch, GrantCatalogue, GrantProject as LoadGrantProject,
  GrantProjects, RefreshGrantCatalogue, RetryGrantProject, StopGrantProject,
} from '../../../wailsjs/go/main/App'
import type { Catalogue, GrantProject, PendingSend, Program, ProjectInput, ProjectSummary, ResearchApproval } from '../../logic/grantProduct'
import { GRANT_PROVIDERS, GRANT_STAGE_LABELS } from '../../logic/grantProduct'
import { providerStyle } from '../../providers'
import { Markdown } from '../../canvas/Markdown'
import { GrantIntake } from './GrantIntake'
import { GrantResearchConfirmation } from './GrantResearchConfirmation'
import { GrantSharingCancelled, useGrantSharingApproval } from './GrantSharingApproval'

interface Props {
  availableProviders: string[]
  onSwitchToCanvas: () => void
}

export function GrantChatWorkspace({ availableProviders, onSwitchToCanvas }: Props) {
  const [catalogue, setCatalogue] = useState<Catalogue | null>(null)
  const [projects, setProjects] = useState<ProjectSummary[]>([])
  const [activeID, setActiveID] = useState<number | null>(null)
  const [project, setProject] = useState<GrantProject | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [tab, setTab] = useState<'team' | 'application' | 'research'>('team')
  const actionInFlight = useRef(false)
  const mounted = useRef(true)
  const sharing = useGrantSharingApproval()
  const receiveProject = useCallback((next: GrantProject) => {
    setProject((previous) => previous?.id === next.id && previous.version > next.version ? previous : next)
    setProjects((previous) => {
      const existing = previous.find((item) => item.id === next.id)
      if (existing && existing.version > next.version) return previous
      const summary: ProjectSummary = { id: next.id, version: next.version, title: next.input.facts.title, program_name: next.input.program.name, stage: next.stage, updated_at: next.updated_at }
      return [summary, ...previous.filter((item) => item.id !== next.id)]
    })
  }, [])

  useEffect(() => {
    mounted.current = true
    let cancelled = false
    void Promise.all([GrantCatalogue(), GrantProjects()]).then(([nextCatalogue, nextProjects]) => {
      if (!cancelled) { setCatalogue(nextCatalogue); setProjects(nextProjects ?? []) }
    }).catch(() => { if (!cancelled) setError('Daemon bağlantısı kurulamadı. Üst çubuktan daemonu başlatın.') })
    return () => { cancelled = true; mounted.current = false }
  }, [])

  useEffect(() => {
    if (activeID === null) return
    let cancelled = false
    let polling = false
    const refresh = async () => {
      if (polling || cancelled) return
      polling = true
      try {
        const next = await LoadGrantProject(activeID)
        if (!cancelled) receiveProject(next)
      } catch (cause) {
        if (!cancelled) setError(`Başvuru durumu okunamadı: ${String(cause)}`)
      } finally { polling = false }
    }
    void refresh()
    const timer = window.setInterval(() => void refresh(), 1500)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [activeID, receiveProject])

  async function action(work: () => Promise<void>, propagate = false) {
    if (actionInFlight.current) return
    actionInFlight.current = true
    setBusy(true)
    setError('')
    setNotice('')
    try { await work() } catch (cause) {
      if (mounted.current) {
        setError(cause instanceof GrantSharingCancelled
          ? 'Paylaşım iptal edildi. Önceki onaylı gönderimler geri alınmaz; yeni gönderim yapılmadı.'
          : String(cause))
      }
      if (propagate) throw cause
    } finally {
      actionInFlight.current = false
      if (mounted.current) setBusy(false)
    }
  }

  async function start(input: ProjectInput) {
    await action(async () => {
      const scope = await sharing.approveScope(JSON.stringify(input, null, 2), [...GRANT_PROVIDERS], input.sensitive_terms)
      const created = await CreateGrantProject({ ...input, scope_approved: true, sensitive_terms: scope.sensitiveTerms })
      if (!mounted.current) return
      receiveProject(created)
      setActiveID(created.id)
      setTab('team')
    }, true)
  }

  async function send(pending: PendingSend) {
    if (!project) return
    const selected = project
    await action(async () => {
      const scope = { providers: [pending.provider], sensitiveTerms: [...selected.input.sensitive_terms] }
      const prompt = await sharing.approveSend(scope, pending.prompt, `${GRANT_STAGE_LABELS[pending.stage] ?? pending.stage} — ${providerStyle(pending.provider).label} / ${pending.role}`)
      const updated = await ApproveGrantSend(selected.id, { key: pending.key, prompt, sensitive_terms: scope.sensitiveTerms })
      if (mounted.current) receiveProject(updated)
    })
  }

  async function approveResearch(approval: ResearchApproval) {
    if (!project) return
    const selected = project
    await action(async () => {
      const scope = await sharing.approveScope(JSON.stringify({ ...selected.input, facts: approval.facts, requirements: approval.requirements, answers: approval.answers }, null, 2), [...GRANT_PROVIDERS], selected.input.sensitive_terms)
      const updated = await ApproveGrantResearch(selected.id, { ...approval, scope_approved: true, sensitive_terms: scope.sensitiveTerms })
      if (mounted.current) receiveProject(updated)
    }, true)
  }

  async function refreshCatalogue() {
    await action(async () => { const updated = await RefreshGrantCatalogue(); if (mounted.current) setCatalogue(updated) }, true)
  }

  async function addProgram(program: Program) {
    await action(async () => { const updated = await AddGrantProgram(program); if (mounted.current) setCatalogue(updated) }, true)
  }

  return <div className="grant-workspace">
    <header className="grant-product-header">
      <div><h1>Başvuru çalışma alanı</h1><p>Claude araştırmacı · Codex yazar · Gemini hakem. Üçü de araştırır, bulguları paylaşır ve çapraz inceleme yapar.</p></div>
      <button className="button" disabled={busy} onClick={onSwitchToCanvas}>Pano</button>
    </header>
    <div className="grant-project-navigation">
      <button className="button" disabled={busy} aria-pressed={activeID === null} onClick={() => { setActiveID(null); setProject(null); setError(''); setNotice('') }}>Yeni başvuru</button>
      <label>Kaydedilmiş başvurular<select value={activeID ?? ''} disabled={busy} onChange={(event) => {
        setProject(null); setActiveID(event.target.value ? Number(event.target.value) : null); setError(''); setNotice('')
      }}><option value="">Başvuru seçin</option>{projects.map((item) => <option key={item.id} value={item.id}>{item.title} — {GRANT_STAGE_LABELS[item.stage] ?? item.stage}</option>)}</select></label>
      <button className="button" disabled={busy} onClick={() => void action(async () => {
        const [nextCatalogue, nextProjects] = await Promise.all([GrantCatalogue(), GrantProjects()])
        if (mounted.current) { setCatalogue(nextCatalogue); setProjects(nextProjects ?? []) }
      })}>Bağlantıyı yenile</button>
    </div>
    {error && <div role="alert" className="grant-product-error">{error}</div>}
    {notice && <div role="status" className="grant-product-notice">{notice}</div>}
    <main className="grant-product-main" aria-busy={busy}>
      {activeID === null && <GrantIntake catalogue={catalogue} availableProviders={availableProviders} busy={busy} onRefreshCatalogue={refreshCatalogue} onAddProgram={addProgram} onStart={start} />}
      {activeID !== null && !project && <p>Kaydedilmiş çalışma yükleniyor…</p>}
      {project && <>
        <section className="grant-project-status">
          <h2>{project.input.facts.title}</h2><p>{project.input.program.name} · {GRANT_STAGE_LABELS[project.stage] ?? project.stage} · Düzeltme turu {project.revision_count}</p>
          <p>Çalışma daemon tarafından saklanır. Pencere kapanırsa onaylanan işler devam edebilir; yeni gönderimler onay bekler.</p>
          {project.error && <p role="alert">{project.error}</p>}
          <div className="grant-project-actions">
            {!['completed', 'cancelled'].includes(project.stage) && <button className="button" disabled={busy} onClick={() => void action(async () => {
              const updated = await StopGrantProject(project.id); if (mounted.current) receiveProject(updated)
            })}>Çalışmayı durdur</button>}
            {project.stage === 'blocked' && <button className="button" disabled={busy} onClick={() => void action(async () => {
              const updated = await RetryGrantProject(project.id); if (mounted.current) receiveProject(updated)
            })}>Engellenen aşamayı yeniden hazırla</button>}
            {project.stage === 'completed' && <button className="button button--primary" disabled={busy} onClick={() => void action(async () => {
              const paths = await ExportGrantDossiers(project.id)
              if (mounted.current && paths?.length) setNotice(`İki Word dosyası kaydedildi: ${paths.join(' · ')}`)
            })}>Başvuru ve AI dosyalarını Word olarak kaydet</button>}
            <button className="button" disabled={busy} onClick={() => void action(async () => {
              const path = await ExportGrantResearch(project.id); if (mounted.current && path) setNotice(`AI çalışma dosyası kaydedildi: ${path}`)
            })}>AI çalışma dosyasını Word olarak kaydet</button>
          </div>
        </section>
        {project.pending.length > 0 && <section className="grant-pending" aria-label="Onay bekleyen gönderimler">
          <h3>Her gönderim ayrı onay bekliyor</h3>
          <p>Bu aşama tüm gerekli yanıtlar bitmeden ilerlemez. Sağlayıcı hataları hazır dosya üretmez.</p>
          {project.pending.map((pending) => <div key={pending.key} className="grant-pending-row">
            <span>{providerStyle(pending.provider).label} · {pending.role} · {GRANT_STAGE_LABELS[pending.stage]}</span>
            <button className="button button--primary" disabled={busy || !availableProviders.includes(pending.provider)} onClick={() => void send(pending)}>Metni incele ve onayla</button>
          </div>)}
        </section>}
        {project.stage === 'research_approval' && <GrantResearchConfirmation key={`${project.id}:${project.version}`} project={project} busy={busy} onConfirm={approveResearch} />}
        <nav className="grant-product-tabs" aria-label="Çalışma görünümü">
          <button className="button" aria-pressed={tab === 'team'} onClick={() => setTab('team')}>AI ekibi ve tartışmalar</button>
          <button className="button" aria-pressed={tab === 'application'} onClick={() => setTab('application')} disabled={!project.application_markdown}>Başvuru {project.stage === 'completed' ? '' : 'taslağı'}</button>
          <button className="button" aria-pressed={tab === 'research'} onClick={() => setTab('research')}>Kaynaklar ve hakem bulguları</button>
        </nav>
        {tab === 'team' && <div className="grant-team-contributions">{project.contributions.length === 0 && <p>Henüz AI gönderimi yapılmadı.</p>}{project.contributions.map((item) => <article key={`${item.turn_id}:${item.provider}`} className="grant-contribution">
          <header><h3>{providerStyle(item.provider).label} · {item.role}</h3><span>{GRANT_STAGE_LABELS[item.stage]} · tur {item.round} · {item.status}{item.activity ? ` · ${item.activity}` : ''}</span></header>
          {item.error && <p role="alert">{item.error}</p>}
          <details><summary>Onaylanan gönderim metni</summary><pre>{item.prompt}</pre></details>
          <div className="grant-contribution-content"><Markdown>{item.content || 'Yanıt bekleniyor…'}</Markdown></div>
        </article>)}</div>}
        {tab === 'application' && <section className="grant-application-preview"><h3>{project.stage === 'completed' ? 'Nihai başvuru' : 'İncelemesi tamamlanmamış taslak'}</h3><Markdown>{project.application_markdown}</Markdown></section>}
        {tab === 'research' && <section className="grant-audit-preview">
          <h3>Kaynaklar</h3>{project.evidence.map((item, index) => <article key={`${item.url}:${index}`}><a href={item.url} target="_blank" rel="noreferrer">{item.title || item.url}</a><p>{item.verified ? 'Sayfa ve alıntı doğrulandı' : 'Doğrulanamadı'} · {item.checked_at}</p><blockquote>{item.quote}</blockquote><p>{item.claim}</p>{item.error && <p>{item.error}</p>}</article>)}
          <h3>Hakem bulguları</h3>{project.issues.length === 0 ? <p>Henüz kayıtlı bulgu yok. Bu ifade tek başına tamamlanmış inceleme anlamına gelmez.</p> : project.issues.map((issue, index) => <article key={index}><strong>{issue.provider} · {issue.severity} · {issue.section_id}</strong><p>{issue.reason}</p><p>Düzeltme: {issue.correction}</p></article>)}
        </section>}
      </>}
    </main>
    {sharing.dialog}
  </div>
}
