import { useState } from 'react'
import type { GrantProject, ResearchApproval, SectionRequirement } from '../../logic/grantProduct'
import { INTAKE_FACT_FIELDS } from '../../logic/grantIntake'

export function GrantResearchConfirmation({ project, busy, onConfirm }: {
  project: GrantProject
  busy: boolean
  onConfirm: (approval: ResearchApproval) => Promise<void>
}) {
  const [facts, setFacts] = useState({ ...project.input.facts })
  const [answers, setAnswers] = useState<Record<string, string>>({})
  const [requirements, setRequirements] = useState<SectionRequirement[]>(project.requirements.map((item) => ({ ...item, requirements: [...item.requirements] })))
  const [confirmed, setConfirmed] = useState(false)
  const [error, setError] = useState('')
  const sources = [...new Set([
    project.input.program.url,
    ...project.evidence.filter((item) => item.verified).map((item) => item.url),
    ...project.input.documents.map((item) => `document:${item.name}`),
  ])]

  function changeSection(index: number, patch: Partial<SectionRequirement>) {
    setRequirements((current) => current.map((section, position) => position === index ? { ...section, ...patch } : section))
    setConfirmed(false)
  }

  async function confirm() {
    setError('')
    try {
      await onConfirm({ version: project.version, facts, answers, requirements, confirmed, scope_approved: false, sensitive_terms: project.input.sensitive_terms })
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
    }
  }

  return <section className="grant-research-confirmation" aria-labelledby="grant-research-confirmation-title">
    <h2 id="grant-research-confirmation-title">Araştırma ve resmi form onayı</h2>
    <p>Yazım başlamadı. Kaynakları, proje bilgilerini ve resmi form şartlarını kontrol edin. AI uzlaşısı doğruluk veya kabul garantisi değildir.</p>
    {project.error && <p role="alert">{project.error}</p>}
    <h3>Kaynak denetimi</h3>
    {project.evidence.length === 0 && <p role="alert">AI araştırması doğrulanabilir kaynak vermedi. Resmi form belgesini ve teknik dayanakları kontrol edin.</p>}
    {project.evidence.map((item, index) => <article key={`${item.url}:${index}`} className="grant-source-result">
      <a href={item.url} target="_blank" rel="noreferrer">{item.title || item.url}</a>
      <strong>{item.verified ? 'Sayfa ve alıntı doğrulandı' : 'Doğrulanamadı'}</strong>
      <blockquote>{item.quote}</blockquote><p>{item.claim}</p>
      <p>{item.checked_at}{item.error ? ` — ${item.error}` : ''}</p>
    </article>)}
    {project.questions.length > 0 && <><h3>Eksik veya çelişkili bilgi soruları</h3>{project.questions.map((question, index) => <label key={`${question}:${index}`}>{question}
      <textarea value={answers[String(index)] ?? ''} onChange={(event) => { setAnswers((current) => ({ ...current, [String(index)]: event.target.value })); setConfirmed(false) }} disabled={busy} />
    </label>)}</>}
    <details><summary>Proje bilgilerini kontrol et ve düzelt</summary>
      {INTAKE_FACT_FIELDS.map(([key, label]) => <label key={key}>{label}<textarea value={facts[key] ?? ''} disabled={busy} onChange={(event) => { setFacts((current) => ({ ...current, [key]: event.target.value })); setConfirmed(false) }} /></label>)}
    </details>
    <h3>Resmi başvuru bölümleri</h3>
    <p>Her bölümü resmi formdan doğrulayın. Bilinmeyen kelime veya karakter sınırı için 0 kullanın; 0 resmi sınır iddiası değildir.</p>
    {requirements.map((section, index) => <fieldset key={index} disabled={busy} className="grant-requirement">
      <legend>Bölüm {index + 1}</legend>
      <label>Bölüm kimliği<input value={section.id} onChange={(event) => changeSection(index, { id: event.target.value })} /></label>
      <label>Resmi başlık<input value={section.title} onChange={(event) => changeSection(index, { title: event.target.value })} /></label>
      <label>Zorunlu içerik (her satıra bir şart)<textarea value={section.requirements.join('\n')} onChange={(event) => changeSection(index, { requirements: event.target.value.split('\n') })} /></label>
      <div className="grant-requirement-limits">
        <label>En az kelime<input type="number" min="0" value={section.min_words ?? 0} onChange={(event) => changeSection(index, { min_words: Number(event.target.value) })} /></label>
        <label>En fazla kelime<input type="number" min="0" value={section.max_words ?? 0} onChange={(event) => changeSection(index, { max_words: Number(event.target.value) })} /></label>
        <label>En az karakter<input type="number" min="0" value={section.min_chars ?? 0} onChange={(event) => changeSection(index, { min_chars: Number(event.target.value) })} /></label>
        <label>En fazla karakter<input type="number" min="0" value={section.max_chars ?? 0} onChange={(event) => changeSection(index, { max_chars: Number(event.target.value) })} /></label>
      </div>
      <label>Resmi dayanak<select value={section.source_url ?? ''} onChange={(event) => changeSection(index, { source_url: event.target.value })}>
        <option value="">Kaynak seçin</option>{sources.map((source) => <option key={source} value={source}>{source}</option>)}
      </select></label>
      <button type="button" className="button" onClick={() => { setRequirements((current) => current.filter((_, position) => position !== index)); setConfirmed(false) }}>Bu bölümü kaldır</button>
    </fieldset>)}
    <button type="button" className="button" disabled={busy || requirements.length >= 60} onClick={() => {
      setRequirements((current) => [...current, { id: `section-${Date.now()}`, title: '', requirements: [], source_url: project.input.program.url }])
      setConfirmed(false)
    }}>Resmi form bölümü ekle</button>
    <label className="grant-sharing-check"><input type="checkbox" checked={confirmed} disabled={busy} onChange={(event) => setConfirmed(event.target.checked)} />
      Bilgileri, kaynakları, soru yanıtlarını ve resmi form şartlarını doğruladım. Başvuru yazımını onaylıyorum.
    </label>
    {error && <p role="alert">{error}</p>}
    <button type="button" className="button button--primary" disabled={busy || !confirmed || requirements.length === 0 || project.questions.some((_, index) => !answers[String(index)]?.trim())} onClick={() => void confirm()}>Paylaşım kapsamını kontrol et ve yazımı başlat</button>
  </section>
}
