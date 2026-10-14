import type { ImportedDocument } from './grantProduct'

export const INTAKE_FACT_FIELDS = [
  ['title', 'Proje başlığı'],
  ['overview', 'Proje özeti'],
  ['applicant_type', 'Başvuru sahibi türü'],
  ['country', 'Ülke'],
  ['applicant_name', 'Başvuru sahibi adı'],
  ['sector', 'Sektör'],
  ['objectives', 'Amaçlar'],
  ['novelty', 'Yenilikçi yön'],
  ['method', 'Yöntem'],
  ['team', 'Ekip'],
  ['budget', 'Bütçe'],
  ['duration', 'Süre'],
  ['outcomes', 'Beklenen sonuçlar'],
  ['market', 'Pazar'],
  ['risks', 'Riskler'],
  ['work_plan', 'İş planı'],
  ['language', 'Başvuru dili'],
] as const

export type FactField = (typeof INTAKE_FACT_FIELDS)[number][0]

const LABELS: Record<FactField, string[]> = {
  title: ['proje başlığı', 'project title', 'başlık'],
  overview: ['proje özeti', 'project overview', 'özet'],
  applicant_type: ['başvuru sahibi türü', 'applicant type'],
  country: ['ülke', 'country'],
  applicant_name: ['başvuru sahibi adı', 'kurum adı', 'applicant name', 'organization'],
  sector: ['sektör', 'sector'],
  objectives: ['amaçlar', 'hedefler', 'objectives'],
  novelty: ['yenilikçi yön', 'yenilik', 'novelty'],
  method: ['yöntem', 'metodoloji', 'method'],
  team: ['ekip', 'team'],
  budget: ['bütçe', 'budget'],
  duration: ['süre', 'duration'],
  outcomes: ['beklenen sonuçlar', 'çıktılar', 'outcomes'],
  market: ['pazar', 'market'],
  risks: ['riskler', 'risks'],
  work_plan: ['iş planı', 'work plan'],
  language: ['başvuru dili', 'application language'],
}

export interface FactCandidate {
  value: string
  source: string
}

export type FactCandidates = Partial<Record<FactField, FactCandidate[]>>

/** Returns only explicit label/value pairs; no values are inferred from prose. */
export function extractFactCandidates(documents: ImportedDocument[]): FactCandidates {
  const candidates: FactCandidates = {}
  for (const document of documents) {
    if (document.error) continue
    for (const line of document.text.split(/\r?\n/)) {
      const match = /^\s*([^:=：]{2,60})\s*[:=：]\s*(\S.*)\s*$/.exec(line)
      if (!match) continue
      const label = match[1].trim().toLocaleLowerCase('tr-TR')
      const field = INTAKE_FACT_FIELDS.find(([key]) => LABELS[key].some((known) => known === label))?.[0]
      if (!field) continue
      const value = match[2].trim()
      const existing = candidates[field] ?? []
      if (!existing.some((candidate) => candidate.value === value && candidate.source === document.name)) {
        candidates[field] = [...existing, { value, source: document.name }]
      }
    }
  }
  return candidates
}

export function rankProgramRecommendations<T extends { name: string; agency: string; country: string; description: string; eligibility: string[]; sections: { title: string; requirements: string[] }[] }>(
  programs: T[],
  preferences: { country: string; applicantType: string; topics: string },
): { program: T; score: number; reasons: string[]; eligibility: 'stated' | 'unknown' }[] {
  const terms = preferences.topics.toLocaleLowerCase('tr-TR').split(/\s+/).filter((term) => term.length > 2)
  return programs.map((program) => {
    const haystack = [program.name, program.agency, program.description, ...program.sections.flatMap((section) => [section.title, ...section.requirements])]
      .join(' ').toLocaleLowerCase('tr-TR')
    const reasons: string[] = []
    let score = 0
    if (preferences.country.trim() && program.country.toLocaleLowerCase('tr-TR') === preferences.country.trim().toLocaleLowerCase('tr-TR')) {
      score += 3
      reasons.push(`Ülke eşleşiyor: ${program.country}`)
    }
    if (preferences.applicantType.trim()) {
      if (program.eligibility.some((item) => item.toLocaleLowerCase('tr-TR').includes(preferences.applicantType.trim().toLocaleLowerCase('tr-TR')))) {
        score += 2
        reasons.push('Uygunluk metninde başvuru sahibi türü geçiyor')
      } else reasons.push('Başvuru sahibi türü uygunluğu katalog metninde teyit edilmedi')
    }
    const topicMatches = terms.filter((term) => haystack.includes(term))
    score += topicMatches.length
    if (topicMatches.length) reasons.push(`Konu terimleri eşleşiyor: ${topicMatches.join(', ')}`)
    return { program, score, reasons, eligibility: program.eligibility.length ? 'stated' as const : 'unknown' as const }
  }).sort((a, b) => b.score - a.score)
}

export function isOfficialHttpsUrl(value: string): boolean {
  try {
    const url = new URL(value)
    return url.protocol === 'https:' && Boolean(url.hostname) && !url.username && !url.password
  } catch {
    return false
  }
}
