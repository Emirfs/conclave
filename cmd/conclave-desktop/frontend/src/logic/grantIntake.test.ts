import { describe, expect, it } from 'vitest'
import type { ImportedDocument } from './grantProduct'
import { extractFactCandidates, rankProgramRecommendations } from './grantIntake'

function imported(name: string, text: string, error?: string): ImportedDocument {
  return { name, kind: 'text', text, warnings: [], error }
}

describe('grant intake facts', () => {
  it('extracts only explicitly labeled facts with provenance and ignores failed imports', () => {
    const candidates = extractFactCandidates([
      imported('proposal.docx', 'Proje başlığı: Güneş ölçümü\nBütçe: 100000 TL'),
      imported('broken.pdf', 'Proje başlığı: Uydurma', 'parse failed'),
    ])
    expect(candidates.title).toEqual([{ value: 'Güneş ölçümü', source: 'proposal.docx' }])
    expect(candidates.budget).toEqual([{ value: '100000 TL', source: 'proposal.docx' }])
    expect(candidates.overview).toBeUndefined()
  })

  it('retains distinct cross-document candidates for conflict review', () => {
    expect(extractFactCandidates([
      imported('first.docx', 'Bütçe: 100000 TL'),
      imported('second.xlsx', 'Bütçe: 150000 TL'),
    ]).budget).toEqual([
      { value: '100000 TL', source: 'first.docx' },
      { value: '150000 TL', source: 'second.xlsx' },
    ])
  })
})

describe('grant programme recommendations', () => {
  it('orders by transparent country, eligibility and topic matches without claiming unknown eligibility', () => {
    const programs = [
      { id: 'a', name: 'General fund', agency: 'Agency', country: 'France', description: '', eligibility: [], sections: [] },
      { id: 'b', name: 'Solar research', agency: 'Agency', country: 'Türkiye', description: '', eligibility: ['SME'], sections: [] },
    ]
    const ranked = rankProgramRecommendations(programs, { country: 'Türkiye', applicantType: 'SME', topics: 'solar' })
    expect(ranked[0].program.id).toBe('b')
    expect(ranked[0].score).toBe(6)
    expect(ranked[0].reasons).toHaveLength(3)
    expect(ranked[1].eligibility).toBe('unknown')
    expect(ranked[1].reasons).toContain('Başvuru sahibi türü uygunluğu katalog metninde teyit edilmedi')
  })
})
