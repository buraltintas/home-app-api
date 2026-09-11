package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Probing is how a brand gets mapped. Most chains publish their shops through an endpoint
// their own store-locator page calls, and finding it is the only part of adding a brand
// that needs a person: the rest is a block of configuration.
//
// This does that looking. It fetches the likely locator pages, reads what comes back for
// the marks of a store list, and reports what it found -- including the endpoints the page
// itself calls, which is usually where the data actually is. Everything goes through the
// same robots-respecting fetcher as a real import, so a probe can never ask for something
// an import would not be allowed to.

// candidatePaths are the addresses Turkish retailers put their store locators at. The list
// is short on purpose: it is a starting guess, not a crawl, and a brand that uses none of
// them is looked at by hand once.
var candidatePaths = []string{
	"/magazalarimiz", "/magazalar", "/magaza-bul", "/magazalarimiz/",
	"/store-locator", "/stores", "/tr/magazalar", "/iletisim/magazalarimiz",
	"/kurumsal/magazalarimiz", "/bayilerimiz", "/bayi-bul", "/satis-noktalari",
}

// endpointPattern finds the URLs a page's own scripts call. A store locator almost always
// names its data endpoint in the markup it ships.
var endpointPattern = regexp.MustCompile(`["'\(]((?:https?://[^"'\s\)]+|/)[^"'\s\)]*(?i:store|magaza|mağaza|bayi|dealer|branch|sube|şube)[^"'\s\)]*)["'\)]`)

// Finding is what a probe saw at one address.
type Finding struct {
	URL      string
	Status   string
	JSON     bool
	Stores   int
	Note     string
	Endpoint []string
}

// Probe looks for a brand's store list and reports what it found, in the order most likely
// to be useful. It writes nothing and decides nothing: mapping a brand stays a judgement a
// person makes, with this as the evidence.
func (f *Fetcher) Probe(ctx context.Context, website string) ([]Finding, error) {
	root, e := url.Parse(strings.TrimSpace(website))
	if e != nil || root.Host == "" {
		return nil, fmt.Errorf("brand has no usable website: %q", website)
	}
	seen := map[string]bool{}
	var findings []Finding
	for _, path := range candidatePaths {
		target := root.Scheme + "://" + root.Host + path
		if seen[target] {
			continue
		}
		seen[target] = true
		findings = append(findings, f.look(ctx, target))
	}
	// Then the endpoints those pages named, which is where the data usually is.
	var discovered []string
	for _, finding := range findings {
		discovered = append(discovered, finding.Endpoint...)
	}
	sort.Strings(discovered)
	for _, endpoint := range discovered {
		target := absolute(root, endpoint)
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		findings = append(findings, f.look(ctx, target))
	}
	sort.SliceStable(findings, func(i, j int) bool { return findings[i].Stores > findings[j].Stores })
	return findings, nil
}

func (f *Fetcher) look(ctx context.Context, target string) Finding {
	finding := Finding{URL: target, Status: "ok"}
	body, e := f.Get(ctx, target)
	if e != nil {
		finding.Status = e.Error()
		return finding
	}
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		finding.JSON = true
		finding.Stores, finding.Note = countStores(trimmed)
		return finding
	}
	// An HTML page is only interesting for what it points at.
	finding.Note = fmt.Sprintf("html, %d KB", len(body)/1024)
	for _, match := range endpointPattern.FindAllStringSubmatch(trimmed, -1) {
		finding.Endpoint = append(finding.Endpoint, match[1])
	}
	finding.Endpoint = unique(finding.Endpoint)
	return finding
}

// countStores reports how many store-shaped objects a JSON body holds, and where they sit.
// "Store-shaped" is deliberately loose: something with a name and either coordinates or an
// address. Guessing the exact field names is the mapping step, not this one.
func countStores(body string) (int, string) {
	var document any
	if e := json.Unmarshal([]byte(body), &document); e != nil {
		return 0, "json, unreadable: " + e.Error()
	}
	best, path := 0, ""
	var walk func(node any, at string, depth int)
	walk = func(node any, at string, depth int) {
		if depth > 6 {
			return
		}
		switch value := node.(type) {
		case []any:
			count := 0
			var fields []string
			for _, item := range value {
				object, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if looksLikeStore(object) {
					count++
					if fields == nil {
						fields = keysOf(object)
					}
				}
			}
			if count > best {
				best, path = count, fmt.Sprintf("%d stores at %q; fields: %s", count, at, strings.Join(fields, ", "))
			}
		case map[string]any:
			for key, child := range value {
				next := key
				if at != "" {
					next = at + "." + key
				}
				walk(child, next, depth+1)
			}
		}
	}
	walk(document, "", 0)
	if best == 0 {
		return 0, "json, no store-shaped list found"
	}
	return best, path
}

func looksLikeStore(object map[string]any) bool {
	named, placed := false, false
	for key, value := range object {
		lower := strings.ToLower(key)
		switch {
		case strings.Contains(lower, "name") || lower == "tanim" || lower == "baslik" || lower == "title":
			if _, ok := value.(string); ok {
				named = true
			}
		case strings.Contains(lower, "lat") || strings.Contains(lower, "enlem") ||
			strings.Contains(lower, "adres") || strings.Contains(lower, "address"):
			placed = true
		}
	}
	return named && placed
}

func keysOf(object map[string]any) []string {
	out := make([]string, 0, len(object))
	for key := range object {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func absolute(root *url.URL, raw string) string {
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		parsed, e := url.Parse(raw)
		// Only ever follow a pointer back to the brand's own host.
		if e != nil || parsed.Host != root.Host {
			return ""
		}
		return raw
	}
	if strings.HasPrefix(raw, "/") {
		return root.Scheme + "://" + root.Host + raw
	}
	return ""
}

func unique(values []string) []string {
	seen := map[string]bool{}
	out := values[:0]
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}
