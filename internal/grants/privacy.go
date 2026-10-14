package grants

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

var credentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)\b(?:Bearer|Basic)\s+[A-Za-z0-9+/_=.:-]+`),
	regexp.MustCompile(`\b(?:sk-(?:proj-)?[A-Za-z0-9_-]{12,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|AIza[A-Za-z0-9_-]{20,})\b`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
	regexp.MustCompile(`(?i)\bhttps?://[^\s/@:]+:[^\s/@]+@[^\s]+`),
}

var identityPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`),
	regexp.MustCompile(`(?i)\bTR\d{2}(?:[ \t]?\d){22}\b`),
	regexp.MustCompile(`\b[A-Z]{2}\d{2}[A-Z0-9]{11,30}\b`),
	regexp.MustCompile(`\b\d{11}\b`),
	regexp.MustCompile(`(?:(?:\+90|0090|0)[ \t-]*)?\(?5\d{2}\)?[ \t-]*\d{3}[ \t-]*\d{2}[ \t-]*\d{2}\b`),
}

var credentialFieldPattern = regexp.MustCompile(`(?i)^(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|token|password|passwd|secret|client[_ -]?secret|authorization|session[_ -]?(?:token|id)|şifre|parola)$`)
var credentialAssignmentPattern = regexp.MustCompile(`(?i)(\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|token|password|passwd|secret|client[_ -]?secret|authorization|session[_ -]?(?:token|id)|şifre|parola)\b["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;]+)`)

// Recognized credentials are removed locally too; never restore these in exports.
func ScrubCredentials(text string) string {
	for _, pattern := range credentialPatterns {
		text = pattern.ReplaceAllString(text, "[GİZLİ ANAHTAR ÇIKARILDI]")
	}
	return credentialAssignmentPattern.ReplaceAllString(text, "[GİZLİ ANAHTAR ÇIKARILDI]")
}

// Decode structured outputs before scrubbing so escaped nested strings cannot hide keys.
func ScrubModelOutput(text string) string {
	plain := strings.TrimSpace(text)
	if strings.HasPrefix(plain, "```") && strings.HasSuffix(plain, "```") {
		if newline := strings.IndexByte(plain, '\n'); newline >= 0 {
			plain = strings.TrimSpace(strings.TrimSuffix(plain[newline+1:], "```"))
		}
	}
	decoder := json.NewDecoder(strings.NewReader(plain))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return ScrubCredentials(text)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return ScrubCredentials(text)
	}
	var scrub func(any) any
	scrub = func(item any) any {
		switch item := item.(type) {
		case string:
			return ScrubCredentials(item)
		case map[string]any:
			for key, child := range item {
				if credentialFieldPattern.MatchString(key) {
					item[key] = "[GİZLİ ANAHTAR ÇIKARILDI]"
				} else {
					item[key] = scrub(child)
				}
			}
		case []any:
			for index, child := range item {
				item[index] = scrub(child)
			}
		}
		return item
	}
	encoded, err := json.Marshal(scrub(value))
	if err != nil {
		return ScrubCredentials(text)
	}
	return string(encoded)
}

func maskPatternsText(text string, patterns []*regexp.Regexp, replacements map[string]string) string {
	text = ScrubCredentials(text)
	mask := func(value string) string {
		digest := sha256.Sum256([]byte(value))
		token := fmt.Sprintf("[MASK_%x]", digest[:8])
		if replacements != nil {
			replacements[token] = value
		}
		return token
	}
	for _, pattern := range patterns {
		text = pattern.ReplaceAllStringFunc(text, mask)
	}
	for _, pattern := range identityPatterns {
		text = pattern.ReplaceAllStringFunc(text, mask)
	}
	return text
}

func compileMaskTerms(terms []string) []*regexp.Regexp {
	ordered := append([]string(nil), terms...)
	sort.SliceStable(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	patterns := make([]*regexp.Regexp, 0, len(ordered))
	for _, term := range ordered {
		term = strings.TrimSpace(term)
		if term != "" && ScrubCredentials(term) == term {
			patterns = append(patterns, regexp.MustCompile("(?i)"+regexp.QuoteMeta(term)))
		}
	}
	return patterns
}

func maskText(text string, terms []string, replacements map[string]string) string {
	return maskPatternsText(text, compileMaskTerms(terms), replacements)
}

func MaskForSharing(text string, terms []string) string { return maskText(text, terms, nil) }

// Restore only masks derived from approved, locally-held project facts/documents.
// Unknown masks (including anything invented by a provider) remain visibly masked.
func RestoreSharedText(text string, input ProjectInput) string {
	return restoreSharedTokens(text, localRestorationValues(input))
}

func localRestorationValues(input ProjectInput) map[string]string {
	replacements := make(map[string]string)
	patterns := compileMaskTerms(input.SensitiveTerms)
	for _, value := range input.Facts {
		maskPatternsText(value, patterns, replacements)
	}
	for _, document := range input.Documents {
		maskPatternsText(document.Name, patterns, replacements)
		maskPatternsText(document.Text, patterns, replacements)
	}
	maskPatternsText(input.Program.Name, patterns, replacements)
	maskPatternsText(input.Program.URL, patterns, replacements)
	return replacements
}

func restoreSharedTokens(text string, replacements map[string]string) string {
	for token, value := range replacements {
		text = strings.ReplaceAll(text, token, value)
	}
	return ScrubCredentials(text)
}

func SanitizeInput(input *ProjectInput) {
	input.Program.SourceText = ScrubCredentials(input.Program.SourceText)
	input.Program.SourceError = ScrubCredentials(input.Program.SourceError)
	for key, value := range input.Facts {
		input.Facts[key] = ScrubCredentials(value)
	}
	for index := range input.Documents {
		document := &input.Documents[index]
		document.Name = ScrubCredentials(document.Name)
		document.Text = ScrubCredentials(document.Text)
		document.Error = ScrubCredentials(document.Error)
		for i := range document.Warnings {
			document.Warnings[i] = ScrubCredentials(document.Warnings[i])
		}
	}
	for index := range input.SensitiveTerms {
		input.SensitiveTerms[index] = ScrubCredentials(input.SensitiveTerms[index])
	}
}
