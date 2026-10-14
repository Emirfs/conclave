package grants

// Program describes a programme or call, not a promise of eligibility or funding.
type Program struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Agency      string               `json:"agency"`
	Country     string               `json:"country"`
	URL         string               `json:"url"`
	Description string               `json:"description"`
	Deadline    string               `json:"deadline,omitempty"`
	Eligibility []string             `json:"eligibility"`
	Sections    []SectionRequirement `json:"sections"`
	Status      string               `json:"status"`
	VerifiedAt  string               `json:"verified_at,omitempty"`
	SourceText  string               `json:"source_text,omitempty"`
	SourceError string               `json:"source_error,omitempty"`
}

type SectionRequirement struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Requirements []string `json:"requirements"`
	MinWords     int      `json:"min_words,omitempty"`
	MaxWords     int      `json:"max_words,omitempty"`
	MinChars     int      `json:"min_chars,omitempty"`
	MaxChars     int      `json:"max_chars,omitempty"`
	SourceURL    string   `json:"source_url,omitempty"`
}

type CatalogueSource struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Country   string `json:"country"`
	CheckedAt string `json:"checked_at,omitempty"`
	Error     string `json:"error,omitempty"`
}

type Catalogue struct {
	Programs    []Program         `json:"programs"`
	Sources     []CatalogueSource `json:"sources"`
	UpdatedAt   string            `json:"updated_at,omitempty"`
	RefreshedAt string            `json:"refreshed_at,omitempty"`
	Coverage    string            `json:"coverage"`
}

type DocumentInput struct {
	Name          string `json:"name"`
	ContentBase64 string `json:"content_base64"`
}

type ImportedDocument struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Text     string   `json:"text"`
	Warnings []string `json:"warnings"`
	Error    string   `json:"error,omitempty"`
}

type SourceEvidence struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	Quote     string `json:"quote"`
	Claim     string `json:"claim"`
	Verified  bool   `json:"verified"`
	CheckedAt string `json:"checked_at,omitempty"`
	Error     string `json:"error,omitempty"`
}

type ProjectInput struct {
	Program               Program            `json:"program"`
	Facts                 map[string]string  `json:"facts"`
	Documents             []ImportedDocument `json:"documents"`
	SensitiveTerms        []string           `json:"sensitive_terms"`
	FactsConfirmed        bool               `json:"facts_confirmed"`
	ScopeApproved         bool               `json:"scope_approved"`
	RequirementsConfirmed bool               `json:"requirements_confirmed"`
}

type Contribution struct {
	Provider       string `json:"provider"`
	Role           string `json:"role"`
	Stage          string `json:"stage"`
	Round          int    `json:"round"`
	ConversationID int64  `json:"conversation_id"`
	TurnID         int64  `json:"turn_id,omitempty"`
	Status         string `json:"status"`
	Prompt         string `json:"prompt"`
	Content        string `json:"content"`
	Error          string `json:"error,omitempty"`
	Activity       string `json:"activity,omitempty"`
}

type PendingSend struct {
	Key      string `json:"key"`
	Provider string `json:"provider"`
	Role     string `json:"role"`
	Stage    string `json:"stage"`
	Prompt   string `json:"prompt"`
}

type ReviewIssue struct {
	SectionID  string `json:"section_id"`
	Severity   string `json:"severity"`
	Reason     string `json:"reason"`
	Correction string `json:"correction"`
	Provider   string `json:"provider"`
}

type Project struct {
	ID                  int64                `json:"id"`
	Version             int                  `json:"version"`
	Input               ProjectInput         `json:"input"`
	ResumeStage         string               `json:"resume_stage,omitempty"`
	Stage               string               `json:"stage"`
	Round               int                  `json:"round"`
	RevisionCount       int                  `json:"revision_count"`
	Conversations       map[string]int64     `json:"conversations"`
	Contributions       []Contribution       `json:"contributions"`
	Pending             []PendingSend        `json:"pending"`
	Evidence            []SourceEvidence     `json:"evidence"`
	Questions           []string             `json:"questions"`
	Requirements        []SectionRequirement `json:"requirements"`
	Issues              []ReviewIssue        `json:"issues"`
	ApplicationMarkdown string               `json:"application_markdown"`
	ResearchMarkdown    string               `json:"research_markdown"`
	Error               string               `json:"error,omitempty"`
	UpdatedAt           string               `json:"updated_at"`
}

type ProjectSummary struct {
	ID          int64  `json:"id"`
	Version     int    `json:"version"`
	Title       string `json:"title"`
	ProgramName string `json:"program_name"`
	Stage       string `json:"stage"`
	UpdatedAt   string `json:"updated_at"`
}

type SendApproval struct {
	Key            string   `json:"key"`
	Prompt         string   `json:"prompt"`
	SensitiveTerms []string `json:"sensitive_terms"`
}

type ResearchApproval struct {
	Version        int                  `json:"version"`
	Requirements   []SectionRequirement `json:"requirements"`
	Facts          map[string]string    `json:"facts"`
	Answers        map[string]string    `json:"answers"`
	Confirmed      bool                 `json:"confirmed"`
	ScopeApproved  bool                 `json:"scope_approved"`
	SensitiveTerms []string             `json:"sensitive_terms"`
}

// Normalize keeps collection-valued wire fields as arrays, never JSON null.
func (project *Project) Normalize() {
	if project.Pending == nil {
		project.Pending = []PendingSend{}
	}
	if project.Contributions == nil {
		project.Contributions = []Contribution{}
	}
	if project.Evidence == nil {
		project.Evidence = []SourceEvidence{}
	}
	if project.Questions == nil {
		project.Questions = []string{}
	}
	if project.Requirements == nil {
		project.Requirements = []SectionRequirement{}
	}
	if project.Issues == nil {
		project.Issues = []ReviewIssue{}
	}
	if project.Input.Documents == nil {
		project.Input.Documents = []ImportedDocument{}
	}
	if project.Input.SensitiveTerms == nil {
		project.Input.SensitiveTerms = []string{}
	}
	if project.Input.Program.Eligibility == nil {
		project.Input.Program.Eligibility = []string{}
	}
	if project.Input.Program.Sections == nil {
		project.Input.Program.Sections = []SectionRequirement{}
	}
	for index := range project.Requirements {
		if project.Requirements[index].Requirements == nil {
			project.Requirements[index].Requirements = []string{}
		}
	}
	for index := range project.Input.Documents {
		if project.Input.Documents[index].Warnings == nil {
			project.Input.Documents[index].Warnings = []string{}
		}
	}
}
