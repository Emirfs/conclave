import { useEffect, useRef, useState, type ChangeEvent, type FormEvent } from 'react'
import { ExtractGrantDocuments } from '../../../wailsjs/go/main/App'
import type { Catalogue, DocumentInput, ImportedDocument, Program, ProjectInput } from '../../logic/grantProduct'
import { GRANT_PROVIDERS } from '../../logic/grantProduct'
import { extractFactCandidates, INTAKE_FACT_FIELDS, isOfficialHttpsUrl, rankProgramRecommendations, type FactCandidate, type FactField } from '../../logic/grantIntake'
import './grantIntake.css'

export interface GrantIntakeProps {
  catalogue: Catalogue | null
  availableProviders: string[]
  busy: boolean
  onRefreshCatalogue: () => Promise<void>
  onAddProgram: (program: Program) => Promise<void>
  onStart: (input: ProjectInput) => Promise<void>
}

const PROVIDER_NAMES: Record<(typeof GRANT_PROVIDERS)[number], string> = {
  claude: 'Claude', openai: 'Codex', gemini: 'Gemini',
}
const ACCEPTED_EXTENSIONS = new Set(['pdf', 'docx', 'xlsx'])

function readFile(file: File): Promise<string> {
  const { promise, resolve, reject } = Promise.withResolvers<string>()
  const reader = new FileReader()
  reader.onerror = () => reject(reader.error ?? new Error('Dosya okunamadı.'))
  reader.onload = () => {
    if (typeof reader.result !== 'string') return reject(new Error('Dosya içeriği okunamadı.'))
    resolve(reader.result.slice(reader.result.indexOf(',') + 1))
  }
  reader.readAsDataURL(file)
  return promise
}

export function GrantIntake({ catalogue, availableProviders, busy, onRefreshCatalogue, onAddProgram, onStart }: GrantIntakeProps) {
  const [query, setQuery] = useState('')
  const [mode, setMode] = useState<'browse' | 'recommendations'>('browse')
  const [preferences, setPreferences] = useState({ country: '', applicantType: '', topics: '' })
  const [selectedProgram, setSelectedProgram] = useState<Program | null>(null)
  const [pendingProgram, setPendingProgram] = useState<{ name: string; agency: string; country: string; url: string } | null>(null)
  const [refreshError, setRefreshError] = useState('')
  const [formError, setFormError] = useState('')
  const [actionBusy, setActionBusy] = useState(false)
  const [manual, setManual] = useState({ name: '', agency: '', country: '', url: '' })
  const [documents, setDocuments] = useState<ImportedDocument[]>([])
  const [fileError, setFileError] = useState('')
  const [uploadBusy, setUploadBusy] = useState(false)
  const [facts, setFacts] = useState<Partial<Record<FactField, string>>>({})
  const [confirmedFields, setConfirmedFields] = useState<Set<FactField>>(new Set())
  const [acknowledgedWarnings, setAcknowledgedWarnings] = useState<Record<string, boolean>>({})
  const [sensitiveTerms, setSensitiveTerms] = useState('')
  const [previewPages, setPreviewPages] = useState<Record<string, number>>({})
  const fileInput = useRef<HTMLInputElement>(null)
  const firstIncompleteFact = INTAKE_FACT_FIELDS.find(([key]) => !facts[key]?.trim() || !confirmedFields.has(key))
  const unresolvedFormulaWarnings = documents.flatMap((document) => document.warnings
    .filter((warning) => /unresolved|formula.*cache|cached.*formula/i.test(warning))
    .map((warning) => ({ document: document.name, warning })))
  const warningsAcknowledged = unresolvedFormulaWarnings.every(({ document, warning }) => acknowledgedWarnings[`${document}:${warning}`])
  const normalizedQuery = query.trim().toLocaleLowerCase('tr-TR')
  const recommendations = rankProgramRecommendations(catalogue?.programs ?? [], preferences)
  const programs = mode === 'recommendations'
    ? recommendations.map((item) => item.program)
    : (catalogue?.programs ?? []).filter((program) => {
      const haystack = [program.name, program.agency, program.country, program.description, ...program.sections.flatMap((section) => [section.title, ...section.requirements])]
        .join(' ').toLocaleLowerCase('tr-TR')
      return !normalizedQuery || haystack.includes(normalizedQuery)
    })
  const candidates = extractFactCandidates(documents)
  const allRequiredProvidersAvailable = GRANT_PROVIDERS.every((provider) => availableProviders.includes(provider))
  const factsComplete = INTAKE_FACT_FIELDS.every(([key]) => Boolean(facts[key]?.trim()))
  const mutationsDisabled = busy || actionBusy || uploadBusy
  useEffect(() => {
    if (!pendingProgram || !catalogue) return
    const official = catalogue.programs.find((program) =>
      program.url === pendingProgram.url &&
      program.name.trim().toLocaleLowerCase('tr-TR') === pendingProgram.name.trim().toLocaleLowerCase('tr-TR') &&
      Boolean(program.id) && Boolean(program.verified_at),
    )
    if (official) {
      setSelectedProgram(official)
      setPendingProgram(null)
    }
  }, [catalogue, pendingProgram])

  async function refreshCatalogue() {
    setRefreshError('')
    try { await onRefreshCatalogue() } catch (error) { setRefreshError(error instanceof Error ? error.message : String(error)) }
  }

  async function handleFiles(event: ChangeEvent<HTMLInputElement>) {
    const files = Array.from(event.currentTarget.files ?? [])
    event.currentTarget.value = ''
    if (files.length === 0) return
    setFileError('')
    const unsupported = files.filter((file) => !ACCEPTED_EXTENSIONS.has(file.name.split('.').pop()?.toLowerCase() ?? ''))
    const accepted = files.filter((file) => ACCEPTED_EXTENSIONS.has(file.name.split('.').pop()?.toLowerCase() ?? ''))
    const existingBytes = documents.reduce((total, document) => total + document.text.length, 0)
    const invalidSizes = accepted.filter((file) => file.size > 20 * 1024 * 1024)
    const batchBytes = accepted.reduce((total, file) => total + file.size, 0)
    const validationErrors = [
      ...(unsupported.length ? [`Desteklenmeyen dosya türü: ${unsupported.map((file) => file.name).join(', ')}`] : []),
      ...(files.length > 10 ? ['Bir seferde en fazla 10 dosya seçin.'] : []),
      ...(invalidSizes.length ? [`Dosya başına 20 MB sınırı aşıldı: ${invalidSizes.map((file) => file.name).join(', ')}`] : []),
      ...(batchBytes > 40 * 1024 * 1024 ? ['Seçilen dosyaların toplamı 40 MB sınırını aşıyor.'] : []),
      ...(existingBytes + batchBytes > 40 * 1024 * 1024 ? ['Toplam yükleme boyutu 40 MB sınırını aşıyor. Önce dosya kaldırın.'] : []),
      ...(documents.length + accepted.length > 20 ? ['Toplam en fazla 20 dosya ekleyin.'] : []),
    ]
    if (validationErrors.length) { setFileError(validationErrors.join(' ')); return }
    setUploadBusy(true)
    try {
      const inputs: DocumentInput[] = await Promise.all(accepted.map(async (file) => ({ name: file.name, content_base64: await readFile(file) })))
      const imported = await ExtractGrantDocuments(inputs)
      setDocuments((current) => [...current.filter((item) => !imported.some((next) => next.name === item.name)), ...imported])
      setConfirmedFields(new Set())
      setAcknowledgedWarnings({})
      setPreviewPages({})
      setFacts((current) => {
        const merged = { ...current }
        for (const [key, choices] of Object.entries(extractFactCandidates(imported)) as [FactField, FactCandidate[]][]) {
          if (!merged[key] && choices.length === 1) merged[key] = choices[0].value
        }
        return merged
      })
    } catch (error) {
      setFileError(error instanceof Error ? error.message : String(error))
    } finally {
      setUploadBusy(false)
    }
  }

  function removeDocument(name: string) {
    setDocuments((current) => current.filter((document) => document.name !== name))
    setConfirmedFields(new Set())
  }

  function editFact(field: FactField, value: string) {
    setFacts((current) => ({ ...current, [field]: value }))
    setConfirmedFields((current) => { const next = new Set(current); next.delete(field); return next })
  }

  async function addManualProgram(event: FormEvent) {
    event.preventDefault()
    if (!manual.name.trim() || !manual.agency.trim() || !manual.country.trim() || !isOfficialHttpsUrl(manual.url.trim())) {
      setFormError('Ad, kurum, ülke ve geçerli HTTPS kaynak adresi gereklidir.')
      return
    }
    setFormError('')
    setActionBusy(true)
    const program: Program = { id: '', name: manual.name.trim(), agency: manual.agency.trim(), country: manual.country.trim(), url: manual.url.trim(), description: '', eligibility: [], sections: [], status: 'unverified' }
    setPendingProgram(program)
    try {
      await onAddProgram(program)
    } catch (error) {
      setPendingProgram(null)
      setFormError(error instanceof Error ? error.message : String(error))
    } finally {
      setActionBusy(false)
    }
  }

  async function startIntake() {
    if (!selectedProgram) { setFormError('Önce katalogdaki doğrulanmış programı seçin.'); return }
    if (!factsComplete || INTAKE_FACT_FIELDS.some(([key]) => !confirmedFields.has(key)) || !warningsAcknowledged) { setFormError('Tüm alanları gözden geçirip onaylayın; hesaplama uyarılarını çözün.'); return }
    if (documents.some((document) => document.error)) { setFormError('Başlamadan önce okunamayan belgeleri kaldırın.'); return }
    setFormError('')
    setActionBusy(true)
    const input: ProjectInput = {
      program: selectedProgram,
      facts: Object.fromEntries(INTAKE_FACT_FIELDS.flatMap(([key]) => facts[key]?.trim() ? [[key, facts[key]!.trim()]] : [])),
      documents,
      sensitive_terms: sensitiveTerms.split(/[,\n]/).map((term) => term.trim()).filter(Boolean),
      facts_confirmed: true,
      scope_approved: false,
      requirements_confirmed: false,
    }
    try { await onStart(input) } catch (error) { setFormError(error instanceof Error ? error.message : String(error)) } finally { setActionBusy(false) }
  }


  return <section className="grant-intake" aria-labelledby="grant-intake-title">
    <header className="grant-intake-header"><div><h1 id="grant-intake-title">Hibe başvurusu oluştur</h1><p>Resmî kaynak seçin, bildiğiniz proje bilgilerini doğrulayın.</p></div>
      <button type="button" onClick={refreshCatalogue} disabled={mutationsDisabled}>Kataloğu yenile</button>
    </header>
    <section className="grant-intake-providers" aria-label="Gerekli sağlayıcılar"><h2>Gerekli sağlayıcılar</h2>{GRANT_PROVIDERS.map((provider) => {
      const available = availableProviders.includes(provider)
      return <div key={provider} className={available ? 'grant-intake-provider available' : 'grant-intake-provider missing'}><strong>{PROVIDER_NAMES[provider]}</strong><span>{available ? 'Hazır' : 'Eksik önkoşul: sağlayıcı kurulumu veya kimlik doğrulaması gerekli.'}</span></div>
    })}</section>
    <section className="grant-intake-catalogue"><h2>Program kataloğu</h2><p>{catalogue?.coverage ?? 'Katalog yüklenmedi; kapsam bilinmiyor.'}</p>
      {catalogue?.updated_at && <p>Katalog güncellemesi: {catalogue.updated_at}</p>}
      <div className="grant-intake-mode"><button type="button" disabled={mutationsDisabled} aria-pressed={mode === 'browse'} onClick={() => setMode('browse')}>Katalogda ara</button><button type="button" disabled={mutationsDisabled} aria-pressed={mode === 'recommendations'} onClick={() => setMode('recommendations')}>Proje temelli öneriler</button></div>
      {mode === 'recommendations' ? <div className="grant-intake-preferences">
        <label>Hedef ülke<input value={preferences.country} onChange={(event) => setPreferences({ ...preferences, country: event.target.value })} /></label>
        <label>Başvuru sahibi türü<input value={preferences.applicantType} onChange={(event) => setPreferences({ ...preferences, applicantType: event.target.value })} /></label>
        <label>Proje konuları<input value={preferences.topics} onChange={(event) => setPreferences({ ...preferences, topics: event.target.value })} /></label>
        <p>Öneriler, katalog metnindeki ülke ve terim eşleşmelerine göre sıralanır; uygunluk garanti edilmez.</p>
      </div> : <label>Ülke, kurum, program veya konu ara<input value={query} onChange={(event) => setQuery(event.target.value)} /></label>}
      {refreshError && <p role="alert">Katalog yenileme başarısız: {refreshError}</p>}
      {catalogue?.sources.map((source) => <div className="grant-intake-source" key={`${source.url}:${source.name}`}><a href={source.url} target="_blank" rel="noreferrer">{source.name} — {source.country}</a><span>{source.checked_at ? `Kontrol: ${source.checked_at}` : 'Kontrol zamanı yok'}</span>{source.error && <span role="alert">Kaynak hatası: {source.error}</span>}</div>)}
      <p>{catalogue ? 'Listelenmeyen programlar bu katalogda bulunmayabilir; kapsam tam değildir.' : 'Program listesi kullanılamıyor.'}</p>
      <div className="grant-intake-programs">{programs.map((program) => {
        const recommendation = recommendations.find((item) => item.program.url === program.url)
        return <article key={`${program.id}:${program.url}`} className="grant-intake-program"><h3>{program.name}</h3><p>{program.agency} · {program.country}</p><p>{program.description || 'Açıklama sağlanmadı.'}</p><a href={program.url} target="_blank" rel="noreferrer">Resmî kaynak</a><span>{program.verified_at ? `Doğrulama: ${program.verified_at}` : 'Kaynak henüz doğrulanmadı'}</span>
          {program.source_error && <p role="alert">Son kaynak kontrolü başarısız: {program.source_error}. Önceki doğrulama tarihi güncel uygunluk anlamına gelmez.</p>}
          {mode === 'recommendations' && recommendation && <div className="grant-intake-recommendation"><strong>Metin eşleşme puanı: {recommendation.score}</strong><p>{recommendation.reasons.join('; ') || 'Tercihlerle eşleşme bulunmadı.'}</p><p>{recommendation.eligibility === 'unknown' ? 'Uygunluk bilgisi katalogda yok; uygunluk bilinmiyor.' : 'Uygunluk metni var; başvuru koşullarını resmî kaynakta doğrulayın.'}</p></div>}
          <button type="button" disabled={mutationsDisabled || !program.id || !program.verified_at} onClick={() => setSelectedProgram(program)} aria-pressed={selectedProgram?.url === program.url}>Seç</button>
        </article>
      })}{programs.length === 0 && <p>Eşleşen kayıt yok; aşağıdan program adresi ekleyin.</p>}</div>
    </section>
    <form className="grant-intake-manual" onSubmit={addManualProgram}><h2>Resmî program ekle</h2><label>Program adı<input value={manual.name} onChange={(event) => setManual({ ...manual, name: event.target.value })} required /></label><label>Kurum<input value={manual.agency} onChange={(event) => setManual({ ...manual, agency: event.target.value })} required /></label><label>Ülke<input value={manual.country} onChange={(event) => setManual({ ...manual, country: event.target.value })} required /></label><label>Resmî HTTPS adresi<input type="url" value={manual.url} onChange={(event) => setManual({ ...manual, url: event.target.value })} required /></label><button type="submit" disabled={mutationsDisabled}>Programı ekle</button>{pendingProgram && <p>Kaynak sunucu tarafından doğrulanıp kataloğa eklenmesi bekleniyor.</p>}</form>
    <section className="grant-intake-project"><h2>Proje bilgileri</h2><p>Yalnızca bildiğiniz bilgileri yazın. Otomatik olmayan alanları kendiniz tamamlayın. Gerçekten ilgisiz alanlara “Uygulanamaz: gerekçe” yazın.</p>
      <label>PDF, DOCX veya XLSX dosyaları<input ref={fileInput} type="file" accept=".pdf,.docx,.xlsx" multiple onChange={handleFiles} disabled={mutationsDisabled} /></label>
      {uploadBusy && <p role="status">Dosyalar okunup ayrıştırılıyor…</p>}
      {fileError && <p role="alert">Dosya aktarım hatası: {fileError}</p>}
      {documents.map((document) => {
        const page = previewPages[document.name] ?? 0
        const pageSize = 12000
        const pageCount = Math.max(1, Math.ceil(document.text.length / pageSize))
        return <article key={document.name} className="grant-intake-document"><strong>{document.name}</strong><span>{document.kind}</span><button type="button" disabled={mutationsDisabled} onClick={() => removeDocument(document.name)}>Dosyayı kaldır</button>{document.error && <p role="alert">Ayrıştırma hatası: {document.error}</p>}{document.warnings.map((warning, index) => <p key={`${warning}:${index}`}>Belirsizlik: {warning}</p>)}{!document.error && <details><summary>Metin incelemesi ({document.text.length} karakter)</summary><pre>{document.text.slice(page * pageSize, (page + 1) * pageSize)}</pre><div><button type="button" disabled={page === 0} onClick={() => setPreviewPages((current) => ({ ...current, [document.name]: page - 1 }))}>Önceki bölüm</button><span>Bölüm {page + 1}/{pageCount}; gösterilen karakterler {page * pageSize + 1}–{Math.min((page + 1) * pageSize, document.text.length)}</span><button type="button" disabled={page + 1 >= pageCount} onClick={() => setPreviewPages((current) => ({ ...current, [document.name]: page + 1 }))}>Sonraki bölüm</button></div></details>}</article>
      })}
      {unresolvedFormulaWarnings.map(({ document, warning }) => <label key={`${document}:${warning}`} className="grant-intake-warning"><input type="checkbox" checked={Boolean(acknowledgedWarnings[`${document}:${warning}`])} disabled={mutationsDisabled} onChange={(event) => setAcknowledgedWarnings((current) => ({ ...current, [`${document}:${warning}`]: event.target.checked }))} />{document}: {warning}. Hesaplanmamış hücre değerlerini doğruladım veya düzelttim.</label>)}
      <div className="grant-intake-facts">{INTAKE_FACT_FIELDS.map(([key, label]) => {
        const choices = candidates[key] ?? []
        const conflict = new Set(choices.map((candidate) => candidate.value)).size > 1
        const candidate = choices.find((choice) => choice.value === facts[key]) ?? choices[0]
        return <div key={key} className="grant-intake-fact"><label>{label}<textarea value={facts[key] ?? ''} onChange={(event) => editFact(key, event.target.value)} disabled={mutationsDisabled} /></label>
          {conflict ? <div role="alert"><strong>Kaynaklar çelişiyor; bir değeri seçin veya düzenleyin.</strong>{choices.map((choice) => <label key={`${choice.source}:${choice.value}`}><input type="radio" name={`candidate-${key}`} checked={facts[key] === choice.value} disabled={mutationsDisabled} onChange={() => editFact(key, choice.value)} />{choice.value} — {choice.source}</label>)}</div> : candidate && <p>Aday kaynak: {candidate.source}; etiket eşleşmesi. İçeriği doğrulayın veya düzenleyin.</p>}
          <label className="grant-intake-confirm"><input type="checkbox" checked={confirmedFields.has(key)} disabled={mutationsDisabled || !facts[key]?.trim()} onChange={(event) => setConfirmedFields((current) => { const next = new Set(current); if (event.target.checked) next.add(key); else next.delete(key); return next })} /> Bu alanı ve varsa kaynak çelişkisini gözden geçirdim</label>
        </div>
      })}</div>
      {firstIncompleteFact && <aside className="grant-intake-question" aria-live="polite"><strong>Eksik bilgi</strong><p>Başvuruya devam etmek için “{INTAKE_FACT_FIELDS.find(([key]) => key === firstIncompleteFact[0])?.[1]}” alanını girin veya onaylayın.</p></aside>}
      <label>Elle maskelenecek ifadeler (her satıra bir ifade)<textarea value={sensitiveTerms} onChange={(event) => setSensitiveTerms(event.target.value)} disabled={mutationsDisabled} /></label>
      {formError && <p role="alert">{formError}</p>}
      {!allRequiredProvidersAvailable && <p>Eksik sağlayıcılar nedeniyle başlatma devre dışı.</p>}
      {!warningsAcknowledged && <p role="alert">Dosyalardaki hesaplama belirsizliğini doğrulayın veya kaynak dosyayı kaldırın.</p>}
      <button type="button" onClick={startIntake} disabled={mutationsDisabled || !allRequiredProvidersAvailable || !factsComplete || INTAKE_FACT_FIELDS.some(([key]) => !confirmedFields.has(key)) || !warningsAcknowledged || !selectedProgram || documents.some((document) => document.error)}>Başvuruyu hazırla</button>
    </section>
  </section>
}
