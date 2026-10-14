export interface SectionRequirement {
  id: string
  title: string
  requirements: string[]
  min_words?: number
  max_words?: number
  min_chars?: number
  max_chars?: number
  source_url?: string
}

export interface Program {
  id: string
  name: string
  agency: string
  country: string
  url: string
  description: string
  deadline?: string
  eligibility: string[]
  sections: SectionRequirement[]
  status: string
  verified_at?: string
  source_text?: string
  source_error?: string
}

export interface CatalogueSource {
  name: string
  url: string
  country: string
  checked_at?: string
  error?: string
}

export interface Catalogue {
  programs: Program[]
  sources: CatalogueSource[]
  updated_at?: string
  refreshed_at?: string
  coverage: string
}

export interface DocumentInput {
  name: string
  content_base64: string
}

export interface ImportedDocument {
  name: string
  kind: string
  text: string
  warnings: string[]
  error?: string
}

export interface ProjectInput {
  program: Program
  facts: Record<string, string>
  documents: ImportedDocument[]
  sensitive_terms: string[]
  facts_confirmed: boolean
  scope_approved: boolean
  requirements_confirmed: boolean
}

export interface Contribution {
  provider: string
  role: string
  stage: string
  round: number
  conversation_id: number
  turn_id?: number
  status: string
  prompt: string
  content: string
  error?: string
  activity?: string
}

export interface PendingSend {
  key: string
  provider: string
  role: string
  stage: string
  prompt: string
}

export interface SourceEvidence {
  url: string
  title: string
  quote: string
  claim: string
  verified: boolean
  checked_at?: string
  error?: string
}

export interface ReviewIssue {
  section_id: string
  severity: string
  reason: string
  correction: string
  provider: string
}

export interface GrantProject {
  id: number
  version: number
  input: ProjectInput
  stage: string
  resume_stage?: string
  round: number
  revision_count: number
  conversations: Record<string, number>
  contributions: Contribution[]
  pending: PendingSend[]
  evidence: SourceEvidence[]
  questions: string[]
  requirements: SectionRequirement[]
  issues: ReviewIssue[]
  application_markdown: string
  research_markdown: string
  error?: string
  updated_at: string
}

export interface ProjectSummary {
  id: number
  version: number
  title: string
  program_name: string
  stage: string
  updated_at: string
}

export interface SendApproval {
  key: string
  prompt: string
  sensitive_terms: string[]
}

export interface ResearchApproval {
  version: number
  requirements: SectionRequirement[]
  facts: Record<string, string>
  answers: Record<string, string>
  confirmed: boolean
  scope_approved: boolean
  sensitive_terms: string[]
}

export const GRANT_PROVIDERS = ['claude', 'openai', 'gemini'] as const

export const GRANT_STAGE_LABELS: Record<string, string> = {
  research: 'Üç bağımsız araştırma',
  discussion: 'Araştırma paylaşımı ve çapraz tartışma',
  research_approval: 'Bilgi ve araştırma onayı bekleniyor',
  writing: 'Başvuru yazımı',
  review: 'Üç bağımsız hakem incelemesi',
  revision: 'Hakem düzeltmeleri',
  completed: 'Doğrulanmış dosya hazır',
  blocked: 'Düzeltme gerekiyor',
  cancelled: 'Durduruldu',
}
