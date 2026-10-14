package grants

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var scriptStylePattern = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>|<style\b[^>]*>.*?</style\s*>`)
var htmlTagPattern = regexp.MustCompile(`(?s)<[^>]*>`)

const maxEvidenceSources = 36
const evidenceWorkers = 4

const maxPublicSourceBytes = 2 << 20

// FetchPublicSource fetches bounded text from a public HTTPS URL.
func FetchPublicSource(ctx context.Context, rawURL string) (string, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 4 {
				return errors.New("too many redirects")
			}
			return validatePublicURL(req.URL)
		},
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(address)
				if err != nil {
					return nil, err
				}
				ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, err
				}
				for _, ip := range ips {
					addr, ok := netip.AddrFromSlice(ip.IP)
					if !ok || !isPublicAddress(addr.Unmap()) {
						return nil, errors.New("source resolves to a non-public address")
					}
				}
				if len(ips) == 0 {
					return nil, errors.New("source host has no addresses")
				}
				dialer := net.Dialer{Timeout: 5 * time.Second}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
			},
		},
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", err
	}
	if err := validatePublicURL(u); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/html,text/plain,application/xhtml+xml,application/pdf,application/json;q=0.8,*/*;q=0.1")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("source returned HTTP %s", resp.Status)
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPublicSourceBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxPublicSourceBytes {
		return "", errors.New("source exceeds 2 MiB limit")
	}
	return decodePublicSource(mediaType, body)
}

func decodePublicSource(mediaType string, body []byte) (string, error) {
	switch mediaType {
	case "application/pdf":
		text, errText := extractPDF(body)
		if errText != "" {
			return "", errors.New(errText)
		}
		if strings.TrimSpace(text) == "" {
			return "", errors.New("PDF contains no extractable text; it may be scanned or empty")
		}
		return text, nil
	}
	if mediaType == "" || (!strings.HasPrefix(mediaType, "text/") && mediaType != "application/xhtml+xml" && mediaType != "application/json") {
		return "", fmt.Errorf("source content type %q is not supported text or PDF", mediaType)
	}
	if !utf8.Valid(body) {
		return "", errors.New("source body is not valid UTF-8 text")
	}
	return string(body), nil
}

func validatePublicURL(u *url.URL) error {
	if u == nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" {
		return errors.New("source URL must use HTTPS")
	}
	if u.User != nil {
		return errors.New("source URL must not contain credentials")
	}
	for key := range u.Query() {
		lower := strings.ToLower(key)
		if credentialFieldPattern.MatchString(key) || lower == "sig" || lower == "signature" || strings.HasPrefix(lower, "x-amz-") || strings.HasPrefix(lower, "x-goog-") {
			return errors.New("source URL must not contain authentication or signed-access parameters")
		}
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return errors.New("source host is not public")
	}
	if ip, err := netip.ParseAddr(host); err == nil && !isPublicAddress(ip.Unmap()) {
		return errors.New("source host is not public")
	}
	return nil
}

func isPublicAddress(ip netip.Addr) bool {
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	reserved := [...]string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "64:ff9b::/96", "64:ff9b:1::/48", "2001:db8::/32", "2001::/23", "2002::/16"}
	for _, raw := range reserved {
		if netip.MustParsePrefix(raw).Contains(ip.Unmap()) {
			return false
		}
	}
	return true
}

// VerifyEvidence fetches each unique source once with bounded concurrency.
func VerifyEvidence(ctx context.Context, evidence []SourceEvidence) []SourceEvidence {
	type fetchResult struct {
		text string
		err  error
	}
	if ctx == nil {
		ctx = context.Background()
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	urls := make([]string, 0, maxEvidenceSources)
	indices := make(map[string]int)
	for i := range evidence {
		evidence[i].Verified = false
		evidence[i].Error = ""
		evidence[i].CheckedAt = time.Now().UTC().Format(time.RFC3339)
		if i >= maxEvidenceSources {
			evidence[i].Error = "at most 36 research sources can be verified at once"
			continue
		}
		if strings.TrimSpace(evidence[i].Quote) == "" || strings.TrimSpace(evidence[i].Claim) == "" {
			evidence[i].Error = "evidence requires a quote and claim"
			continue
		}
		if len([]rune(strings.TrimSpace(evidence[i].Quote))) < 20 {
			evidence[i].Error = "quoted evidence must contain at least 20 characters"
			continue
		}
		if _, ok := indices[evidence[i].URL]; !ok {
			indices[evidence[i].URL] = len(urls)
			urls = append(urls, evidence[i].URL)
		}
	}
	results := make([]fetchResult, len(urls))
	type job struct {
		index int
		url   string
	}
	jobs := make(chan job, len(urls))
	for index, rawURL := range urls {
		jobs <- job{index: index, url: rawURL}
	}
	close(jobs)
	var workers sync.WaitGroup
	for range evidenceWorkers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range jobs {
				results[item.index].text, results[item.index].err = FetchPublicSource(fetchCtx, item.url)
			}
		}()
	}
	workers.Wait()
	for i := range evidence {
		item := &evidence[i]
		if item.Error != "" {
			continue
		}
		result := results[indices[item.URL]]
		if result.err != nil {
			item.Error = result.err.Error()
		} else if !strings.Contains(normalizeEvidence(visibleText(result.text)), normalizeEvidence(item.Quote)) {
			item.Error = "quoted text was not found in the cited source"
		} else {
			item.Verified = true
		}
	}
	return evidence
}

func visibleText(value string) string {
	value = scriptStylePattern.ReplaceAllString(value, " ")
	value = htmlTagPattern.ReplaceAllString(value, " ")
	return html.UnescapeString(value)
}

// PublicSourceText returns visible page text, excluding markup and script/style bodies.
func PublicSourceText(raw string) string {
	return strings.TrimSpace(visibleText(raw))
}

// BoundedPublicSourceText refuses oversized visible text rather than truncating it.
func BoundedPublicSourceText(raw string, limit int) (string, error) {
	text := PublicSourceText(raw)
	if limit > 0 && len(text) > limit {
		return "", fmt.Errorf("visible source text exceeds %d-byte limit", limit)
	}
	return text, nil
}

func normalizeEvidence(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(html.UnescapeString(value)), " "))
}
