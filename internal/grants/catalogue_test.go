package grants

import (
	"context"
	"net/netip"
	"strings"
	"testing"
)

func TestPublicAddressRejectsPrivateAndReserved(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "10.0.0.1", "169.254.1.1", "192.0.2.8", "198.51.100.2", "203.0.113.7", "100.64.0.1", "2001:db8::1", "64:ff9b::7f00:1", "2002:7f00:1::1"} {
		if isPublicAddress(netip.MustParseAddr(value)) {
			t.Errorf("accepted non-public address %s", value)
		}
	}
	if !isPublicAddress(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("rejected public address")
	}
}

func TestFetchPublicSourceRejectsUnsafeURLs(t *testing.T) {
	for _, raw := range []string{"http://example.com", "https://user:pass@example.com", "https://127.0.0.1", "https://localhost", "https://example.com/?token=private-value"} {
		if _, err := FetchPublicSource(context.Background(), raw); err == nil {
			t.Errorf("accepted unsafe URL %q", raw)
		}
	}
}

func TestCatalogueRefreshReplacesStaleProgrammeWithoutLosingCustomRecord(t *testing.T) {
	old := Program{ID: "same", Name: "Example", URL: "https://example.org/call", SourceText: "Stale source", VerifiedAt: "2026-01-01"}
	fresh := old
	fresh.SourceText = "Updated official requirements"
	fresh.VerifiedAt = "2026-10-01"
	custom := Program{ID: "custom", Name: "Custom", URL: "https://example.net/call"}
	programs := mergePrograms([]Program{old, custom, fresh})
	for _, program := range programs {
		if program.ID == "same" && (program.SourceText != fresh.SourceText || program.VerifiedAt != fresh.VerifiedAt) {
			t.Fatal("refresh retained stale source")
		}
	}
	if len(programs) != 2 || programs[1].ID != "custom" {
		t.Fatal("refresh lost custom programme or duplicated stale version")
	}
}

func TestExtractCandidatesMatchesOfficialCallLinks(t *testing.T) {
	source := &CatalogueSource{Name: "Agency", URL: "https://grants.example.gov/", Country: "Country"}
	html := `<a href="/calls/one">Open funding call</a><a href="/privacy">Privacy and grant policy</a><a href="https://elsewhere.test/grant">Grant elsewhere</a><a href="/image.png">call for proposal</a>`
	found, err := extractCandidates(source, html)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].URL != "https://grants.example.gov/calls/one" {
		t.Fatalf("unexpected candidates: %#v", found)
	}
	if found[0].Deadline != "" || len(found[0].Eligibility) != 0 || found[0].Status != "candidate" {
		t.Fatalf("candidate contains inferred facts: %#v", found[0])
	}
}

func TestPublicSourceTextRemovesMarkupAndRejectsOverflow(t *testing.T) {
	raw := `<html><script>hidden grant fake</script><style>.secret{}</style><p>A real funding call &amp; award.</p></html>`
	text := PublicSourceText(raw)
	if text != "A real funding call & award." {
		t.Fatalf("unexpected visible text %q", text)
	}
	if _, err := BoundedPublicSourceText(raw, 8); err == nil {
		t.Fatal("expected oversized visible text to fail without truncation")
	}
}

func TestVerifyEvidenceRejectsShortQuoteWithoutFetching(t *testing.T) {
	evidence := VerifyEvidence(context.Background(), []SourceEvidence{{
		URL: "http://unsafe.invalid", Claim: "Claim", Quote: "short quote",
	}})
	if len(evidence) != 1 || evidence[0].Verified || !strings.Contains(evidence[0].Error, "20 characters") {
		t.Fatalf("short quote was not rejected: %#v", evidence)
	}
}

func TestDecodePublicSourceAcceptsHTMLAndRejectsUnsupportedMedia(t *testing.T) {
	raw := []byte(`<a href="/funding">Funding call</a>`)
	got, err := decodePublicSource("text/html", raw)
	if err != nil || string(raw) != got {
		t.Fatalf("HTML must remain raw for catalogue link extraction: %q, %v", got, err)
	}
	if _, err := decodePublicSource("application/octet-stream", []byte("binary")); err == nil {
		t.Fatal("expected unsupported binary media to fail")
	}
	if _, err := decodePublicSource("application/pdf", []byte("%PDF malformed")); err == nil {
		t.Fatal("expected malformed PDF to fail")
	}
}
