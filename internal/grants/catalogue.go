package grants

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

var anchorPattern = regexp.MustCompile(`(?is)<a\b[^>]*href\s*=\s*["']([^"'#]+)["'][^>]*>(.*?)</a\s*>`)
var tagPattern = regexp.MustCompile(`(?is)<[^>]+>`)
var candidateWords = []string{"grant", "funding", "opportunity", "call for", "open call", "tender", "award", "programme", "program", "destek", "çağrı", "hibe"}
var ignoredLinkWords = []string{"privacy", "cookie", "contact", "login", "sign in", "accessibility", "facebook", "twitter", "linkedin", "youtube"}

// seedSources lists official portals only; a portal's presence does not imply
// that it has an open call or that an applicant is eligible.
var seedSources = []CatalogueSource{
	{Name: "Türkiye - TÜBİTAK", URL: "https://tubitak.gov.tr/", Country: "Türkiye"},
	{Name: "Türkiye - KOSGEB", URL: "https://www.kosgeb.gov.tr/", Country: "Türkiye"},
	{Name: "European Union - Funding & Tenders", URL: "https://ec.europa.eu/info/funding-tenders/opportunities/portal/", Country: "European Union"},
	{Name: "United Kingdom - Find a grant", URL: "https://www.find-government-grants.service.gov.uk/", Country: "United Kingdom"},
	{Name: "United States - Grants.gov", URL: "https://www.grants.gov/", Country: "United States"},
	{Name: "Canada - Grants and funding", URL: "https://www.canada.ca/en/government/grants-funding.html", Country: "Canada"},
	{Name: "International - UN Partner Portal", URL: "https://www.unpartnerportal.org/", Country: "International"},
}

// SeedCatalogue returns a conservative baseline without inventing programme
// deadlines, eligibility, or active calls.
func SeedCatalogue() Catalogue {
	sources := append([]CatalogueSource(nil), seedSources...)
	return Catalogue{
		Programs: []Program{},
		Sources:  sources,
		Coverage: "Official portals listed for Türkiye, European Union, United Kingdom, United States, Canada, and international opportunities. Portals are not exhaustive; live calls, deadlines, eligibility, and award status require confirmation at the official source. No verified programme records are seeded.",
	}
}

// RefreshCatalogue discovers conservative same-portal call candidates.
func RefreshCatalogue(ctx context.Context, previous Catalogue) (Catalogue, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	seed := SeedCatalogue()
	old := make(map[string]CatalogueSource, len(previous.Sources))
	for _, source := range previous.Sources {
		old[source.URL] = source
	}
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	var discovered []Program
	for i := range seed.Sources {
		source := &seed.Sources[i]
		document, err := FetchPublicSource(ctx, source.URL)
		source.CheckedAt = checkedAt
		if err != nil {
			source.Error = err.Error()
			if prior, ok := old[source.URL]; ok {
				source.CheckedAt = prior.CheckedAt
			}
			continue
		}
		candidates, err := extractCandidates(source, document)
		if err != nil {
			source.Error = err.Error()
			continue
		}
		discovered = append(discovered, candidates...)
	}
	seed.UpdatedAt = checkedAt
	seed.RefreshedAt = checkedAt
	seed.Programs = mergePrograms(append(discovered, previous.Programs...))
	refreshPrograms(ctx, seed.Programs, checkedAt)
	return seed, nil
}

func refreshPrograms(ctx context.Context, programs []Program, checkedAt string) {
	byURL := make(map[string][]int, len(programs))
	for index := range programs {
		byURL[programs[index].URL] = append(byURL[programs[index].URL], index)
	}
	jobs := make(chan string, len(byURL))
	for rawURL := range byURL {
		jobs <- rawURL
	}
	close(jobs)
	var workers sync.WaitGroup
	for range evidenceWorkers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for rawURL := range jobs {
				raw, err := FetchPublicSource(ctx, rawURL)
				var text string
				if err == nil {
					text, err = BoundedPublicSourceText(raw, 90<<10)
				}
				for _, index := range byURL[rawURL] {
					programs[index].SourceError = ""
					if err != nil {
						programs[index].SourceError = err.Error()
					} else {
						programs[index].SourceText = text
						programs[index].VerifiedAt = checkedAt
					}
				}
			}
		}()
	}
	workers.Wait()
}

func extractCandidates(source *CatalogueSource, document string) ([]Program, error) {
	base, err := url.Parse(source.URL)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var found []Program
	for _, match := range anchorPattern.FindAllStringSubmatch(document, -1) {
		title := strings.TrimSpace(html.UnescapeString(tagPattern.ReplaceAllString(match[2], " ")))
		title = strings.Join(strings.Fields(title), " ")
		if title == "" || len(title) > 240 || isIgnoredLink(title) || !looksLikeCandidate(title) {
			continue
		}
		link, err := url.Parse(html.UnescapeString(strings.TrimSpace(match[1])))
		if err != nil {
			continue
		}
		resolved := base.ResolveReference(link)
		if !strings.EqualFold(resolved.Scheme, "https") || !strings.EqualFold(resolved.Hostname(), base.Hostname()) || !officialHTTPS(resolved.String()) {
			continue
		}
		path := strings.ToLower(resolved.Path)
		if strings.HasSuffix(path, ".pdf") || strings.HasSuffix(path, ".png") || strings.HasSuffix(path, ".jpg") || strings.HasSuffix(path, ".jpeg") || strings.HasSuffix(path, ".svg") || strings.HasSuffix(path, ".zip") || strings.HasSuffix(path, ".docx") || strings.HasSuffix(path, ".xlsx") || strings.HasSuffix(path, ".css") || strings.HasSuffix(path, ".js") {
			continue
		}
		resolved.Fragment = ""
		target := resolved.String()
		if seen[target] {
			continue
		}
		seen[target] = true
		found = append(found, Program{Name: title, Agency: source.Name, Country: source.Country, URL: target, Description: "Candidate discovered from an official portal link; confirm details at source.", Eligibility: []string{}, Sections: []SectionRequirement{}, Status: "candidate"})
		if len(found) == 8 {
			break
		}
	}
	return found, nil
}

func looksLikeCandidate(value string) bool {
	value = strings.ToLower(value)
	for _, word := range candidateWords {
		if strings.Contains(value, word) {
			return true
		}
	}
	return false
}

func isIgnoredLink(value string) bool {
	value = strings.ToLower(value)
	for _, word := range ignoredLinkWords {
		if strings.Contains(value, word) {
			return true
		}
	}
	return false
}

func appendCoverageError(current, next string) string {
	if current == "" {
		return next
	}
	return current + "; " + next
}

func mergePrograms(programs []Program) []Program {
	out := make([]Program, 0, len(programs))
	seen := make(map[string]int, len(programs))
	for _, program := range programs {
		if strings.TrimSpace(program.Name) == "" || !officialHTTPS(program.URL) {
			continue
		}
		if program.ID == "" {
			sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(program.URL + "\n" + program.Name))))
			program.ID = hex.EncodeToString(sum[:8])
		}
		if index, ok := seen[program.ID]; ok {
			out[index] = program
			continue
		}
		seen[program.ID] = len(out)
		out = append(out, program)
	}
	return out
}

func officialHTTPS(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && strings.EqualFold(u.Scheme, "https") && u.Hostname() != "" && u.User == nil && !strings.Contains(fmt.Sprint(u.Hostname()), " ")
}
