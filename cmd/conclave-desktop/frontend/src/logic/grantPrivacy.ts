export interface MaskedGrantText {
  text: string
  maskedCount: number
}

/** Best-effort detection, not a substitute for reviewing the outgoing text. */
export function maskGrantText(source: string, sensitiveTerms: readonly string[] = []): MaskedGrantText {
  let text = source
  let maskedCount = 0
  const replace = (pattern: RegExp, label: string) => {
    text = text.replace(pattern, () => {
      maskedCount++
      return `[${label}]`
    })
  }

  // Remove credentials before other patterns can fragment them.
  replace(/-----BEGIN (?:[A-Z ]*PRIVATE KEY)-----[\s\S]*?-----END (?:[A-Z ]*PRIVATE KEY)-----/g, 'GİZLİ ANAHTAR')
  replace(/\b(?:Bearer|Basic)\s+[A-Za-z0-9+/_=.:-]+/gi, 'KİMLİK DOĞRULAMA')
  replace(/\b(?:sk-(?:proj-)?[A-Za-z0-9_-]{12,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|AIza[A-Za-z0-9_-]{20,})\b/g, 'GİZLİ ANAHTAR')
  replace(/\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b/g, 'OTURUM BELİRTECİ')
  replace(/\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|passwd|secret|şifre|parola)\s*[:=]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;]+)/gi, 'GİZLİ BİLGİ')
  replace(/\bhttps?:\/\/[^\s/@:]+:[^\s/@]+@[^\s]+/gi, 'KİMLİK BİLGİLİ ADRES')
  replace(/\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/gi, 'E-POSTA')
  replace(/\bTR\d{2}(?:[ \t]?\d){22}\b/gi, 'IBAN')
  replace(/\b[A-Z]{2}\d{2}[A-Z0-9]{11,30}\b/g, 'IBAN')
  replace(/(?<!\d)\d{11}(?!\d)/g, 'KİMLİK NUMARASI')
  replace(/(?<!\d)(?:(?:\+90|0090|0)[ \t-]*)?\(?5\d{2}\)?[ \t-]*\d{3}[ \t-]*\d{2}[ \t-]*\d{2}(?!\d)/g, 'TELEFON')

  // Names, addresses, trade secrets and unsupported credentials need explicit terms.
  // Longest first prevents a short term exposing the remainder of a longer one.
  for (const term of [...new Set(sensitiveTerms.map((value) => value.trim()).filter(Boolean))]
    .sort((a, b) => b.length - a.length)) {
    const escaped = term.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    replace(new RegExp(escaped, 'giu'), 'ÖZEL BİLGİ')
  }
  return { text, maskedCount }
}

export function parseSensitiveTerms(value: string): string[] {
  return value.split(/\r?\n/).map((term) => term.trim()).filter(Boolean)
}
