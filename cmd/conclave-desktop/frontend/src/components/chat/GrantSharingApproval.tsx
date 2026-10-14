import { useEffect, useRef, useState } from 'react'
import { maskGrantText, parseSensitiveTerms } from '../../logic/grantPrivacy'
import { providerStyle } from '../../providers'

export interface GrantSharingScope {
  providers: string[]
  sensitiveTerms: string[]
}

interface ApprovalRequest {
  kind: 'scope' | 'send'
  label: string
  source: string
  scope: GrantSharingScope
  resolve: (result: { text: string; sensitiveTerms: string[] } | null) => void
}

export class GrantSharingCancelled extends Error {
  constructor() {
    super('Veri paylaşımı iptal edildi. Yeni bir gönderim yapılmadı.')
  }
}

/** Approvals are local, transient UI decisions, never saved as blanket consent. */
export function useGrantSharingApproval() {
  const [request, setRequest] = useState<ApprovalRequest | null>(null)
  const pending = useRef<ApprovalRequest | null>(null)
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
      pending.current?.resolve(null)
      pending.current = null
    }
  }, [])

  const ask = async (kind: ApprovalRequest['kind'], label: string, source: string, scope: GrantSharingScope) => {
    if (!mounted.current || pending.current) throw new GrantSharingCancelled()
    const { promise, resolve } = Promise.withResolvers<{ text: string; sensitiveTerms: string[] } | null>()
    const next = { kind, label, source, scope, resolve }
    pending.current = next
    setRequest(next)
    const result = await promise
    if (!result || !mounted.current) throw new GrantSharingCancelled()
    return result
  }

  const finish = (result: { text: string; sensitiveTerms: string[] } | null) => {
    const current = pending.current
    pending.current = null
    setRequest(null)
    current?.resolve(result)
  }

  return {
    async approveScope(source: string, providers: string[], initialTerms: readonly string[] = []): Promise<GrantSharingScope> {
      const scope: GrantSharingScope = { providers: [...providers], sensitiveTerms: [...initialTerms] }
      const approved = await ask('scope', 'Proje paylaşım kapsamı', source, scope)
      scope.sensitiveTerms = approved.sensitiveTerms
      return scope
    },
    async approveSend(scope: GrantSharingScope, source: string, label: string): Promise<string> {
      const approved = await ask('send', label, source, scope)
      // Additional masks narrow the scope and remain active for subsequent sends.
      scope.sensitiveTerms = approved.sensitiveTerms
      return approved.text
    },
    dialog: request ? <GrantSharingApproval key={`${request.kind}:${request.label}`} request={request} onFinish={finish} /> : null,
  }
}

function GrantSharingApproval({ request, onFinish }: {
  request: ApprovalRequest
  onFinish: (result: { text: string; sensitiveTerms: string[] } | null) => void
}) {
  const [terms, setTerms] = useState('')
  const [checked, setChecked] = useState(false)
  const additionalTerms = parseSensitiveTerms(terms)
  const sensitiveTerms = [...request.scope.sensitiveTerms, ...additionalTerms]
  const masked = maskGrantText(request.source, sensitiveTerms)
  const isScope = request.kind === 'scope'
  const textRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => { textRef.current?.focus() }, [])
  useEffect(() => {
    const dialog = textRef.current?.closest('[role="dialog"]')
    if (!(dialog instanceof HTMLElement)) return
    const trapFocus = (event: KeyboardEvent) => {
      if (event.key !== 'Tab') return
      const controls = [...dialog.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), textarea:not([disabled]), a[href]')]
      const first = controls[0]
      const last = controls.at(-1)
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last?.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first?.focus()
      }
    }
    dialog.addEventListener('keydown', trapFocus)
    return () => dialog.removeEventListener('keydown', trapFocus)
  }, [])
  useEffect(() => {
    const cancel = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        onFinish(null)
      }
    }
    window.addEventListener('keydown', cancel)
    return () => window.removeEventListener('keydown', cancel)
  }, [onFinish])

  return (
    <div className="grant-modal-overlay grant-sharing-overlay">
      <section className="grant-modal-card grant-modal-card--wide" role="dialog" aria-modal="true" aria-labelledby="grant-sharing-title">
        <div className="grant-modal-header">
          <div>
            <h2 id="grant-sharing-title" className="grant-modal-title">{isScope ? 'Paylaşım kapsamını onaylayın' : 'AI gönderimini onaylayın'}</h2>
            <p className="grant-modal-subtitle">{request.label}</p>
          </div>
          <button className="grant-modal-close" aria-label="Paylaşımı iptal et" onClick={() => onFinish(null)}>Kapat</button>
        </div>
        <div className="grant-modal-body grant-sharing-body">
          <p><strong>Alıcılar:</strong> {request.scope.providers.map((provider) => providerStyle(provider).label).join(', ')}</p>
          {isScope && <p>Proje bilgileri, yüklenen belgeler, program kaynakları ve AI yanıtları kapsam içindedir. Yerel daemon proje bilgilerini bu bilgisayarda saklar. Tanınan kimlik bilgileri çıkarılır; kişisel bilgiler AI gönderiminde maskelenir. Nihai başvuru Word dosyasında yerel proje bilgileri kullanılabilir. Web araştırması herkese açık kaynaklara erişebilir. Her gönderim ayrıca onayınıza sunulur.</p>}
          <p>E-posta, telefon, kimlik numarası, IBAN ve bazı kimlik bilgileri otomatik maskelenir. Adlar, adresler ve ticari sırlar otomatik bulunamayabilir. Önizlemeyi inceleyin; ek bilgileri aşağıda işaretleyin.</p>
          <label htmlFor="grant-sensitive-terms">Ek maskelenecek bilgiler (her satıra bir ad, adres veya gizli ifade)</label>
          <textarea id="grant-sensitive-terms" className="grant-sharing-terms" rows={3} value={terms} onChange={(event) => { setTerms(event.target.value); setChecked(false) }} />
          <label htmlFor="grant-sharing-preview">{isScope ? 'Maskelenmiş proje kapsamı' : 'Gönderilecek tam metin'} — {masked.maskedCount} maskeleme</label>
          <textarea ref={textRef} id="grant-sharing-preview" className="grant-sharing-preview" readOnly value={masked.text} spellCheck={false} />
          {!isScope && <p>Bu metin mevcut konuşmaya eklenir. Alıcılar daha önce onaylanan konuşma geçmişini de kullanabilir. İptal, önceki gönderimleri geri almaz.</p>}
          <label className="grant-sharing-check">
            <input type="checkbox" checked={checked} onChange={(event) => setChecked(event.target.checked)} />
            {isScope ? 'Kapsamı ve maskeleme sınırlarını inceledim. Bu çalışma için kapsamı onaylıyorum.' : 'Metni ve alıcıları inceledim. Yalnızca bu gönderime izin veriyorum.'}
          </label>
        </div>
        <div className="grant-modal-footer grant-sharing-actions">
          <button className="button" onClick={() => onFinish(null)}>İptal et</button>
          <button className="button button--primary" disabled={!checked} onClick={() => onFinish({ text: masked.text, sensitiveTerms })}>
            {isScope ? 'Kapsamı onayla' : 'Bu metni gönder'}
          </button>
        </div>
      </section>
    </div>
  )
}
