package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
)

// HTMLLocator reads a store list a chain renders as markup, with no endpoint behind it.
//
// Plenty of chains publish this way, and the list is as complete as any JSON one -- Özdilek
// prints every shop's name, address and telephone into the page. What it usually lacks is a
// coordinate, which the resolver then supplies from the town; a shop placed at its
// district's centre is a few kilometres out rather than absent, and the decision is recorded
// as such on the row.
//
// The configuration is regular expressions, not CSS selectors, for one reason: a selector
// library is a second parser with its own opinions about broken markup, and these pages are
// broken in ways nobody has catalogued. A pattern that matches what is actually on the page
// is checked by looking at what it produced.
type HTMLLocator struct {
	spec    BrandSpec
	config  HTMLLocatorConfig
	fetcher *Fetcher
	row     *regexp.Regexp
	fields  map[string]*regexp.Regexp
}

// HTMLLocatorConfig is what the registry stores for such a brand. Row delimits one shop;
// every other pattern is matched inside that block and needs exactly one capturing group.
type HTMLLocatorConfig struct {
	URL  string   `json:"url"`
	URLs []string `json:"urls"`
	// Row is the pattern that isolates one shop's block of markup. Everything else is
	// matched within it, so a pattern that is too broad silently mixes two shops' fields.
	Row string `json:"row"`
	// ID is optional: where a page carries no identifier, one is derived from the row.
	ID          string `json:"id"`
	Name        string `json:"name"`
	Address     string `json:"address"`
	City        string `json:"city"`
	District    string `json:"district"`
	Phone       string `json:"phone"`
	Website     string `json:"website"`
	Latitude    string `json:"latitude"`
	Longitude   string `json:"longitude"`
	Coordinates string `json:"coordinates"`
}

func NewHTMLLocator(spec BrandSpec, fetcher *Fetcher) (*HTMLLocator, error) {
	var config HTMLLocatorConfig
	if len(spec.LocatorConfig) > 0 {
		if e := json.Unmarshal(spec.LocatorConfig, &config); e != nil {
			return nil, fmt.Errorf("%s locator config: %w", spec.Slug, e)
		}
	}
	if config.URL == "" && len(config.URLs) == 0 {
		return nil, fmt.Errorf("%s has no locator url", spec.Slug)
	}
	if config.Row == "" {
		return nil, fmt.Errorf("%s has no row pattern", spec.Slug)
	}
	locator := &HTMLLocator{spec: spec, config: config, fetcher: fetcher, fields: map[string]*regexp.Regexp{}}
	var e error
	if locator.row, e = compileOneGroup(spec.Slug, "row", config.Row); e != nil {
		return nil, e
	}
	for name, pattern := range map[string]string{
		"id": config.ID, "name": config.Name, "address": config.Address,
		"city": config.City, "district": config.District, "phone": config.Phone,
		"website": config.Website, "latitude": config.Latitude,
		"longitude": config.Longitude, "coordinates": config.Coordinates,
	} {
		if pattern == "" {
			continue
		}
		if locator.fields[name], e = compileOneGroup(spec.Slug, name, pattern); e != nil {
			return nil, e
		}
	}
	return locator, nil
}

func compileOneGroup(slug, field, pattern string) (*regexp.Regexp, error) {
	compiled, e := regexp.Compile(pattern)
	if e != nil {
		return nil, fmt.Errorf("%s %s pattern: %w", slug, field, e)
	}
	if compiled.NumSubexp() != 1 {
		return nil, fmt.Errorf("%s %s pattern needs exactly one capturing group", slug, field)
	}
	return compiled, nil
}

func (l *HTMLLocator) Brand() BrandSpec { return l.spec }

func (l *HTMLLocator) Fetch(ctx context.Context) ([]RawStore, error) {
	addresses := l.config.URLs
	if len(addresses) == 0 {
		addresses = []string{l.config.URL}
	}
	var out []RawStore
	for _, address := range addresses {
		body, e := l.fetcher.Get(ctx, address)
		if e != nil {
			return nil, e
		}
		page := unprotectEmails(body)
		blocks := l.row.FindAllSubmatch(page, -1)
		if len(blocks) == 0 {
			return nil, fmt.Errorf("%s: no row matched at %s", l.spec.Slug, address)
		}
		for _, block := range blocks {
			row := l.mapBlock(string(block[1]))
			// A block with no name is markup that happened to match the row pattern.
			if row.Name == "" {
				continue
			}
			out = append(out, row)
		}
	}
	return out, nil
}

func (l *HTMLLocator) mapBlock(block string) RawStore {
	field := func(name string) string {
		pattern, ok := l.fields[name]
		if !ok {
			return ""
		}
		match := pattern.FindStringSubmatch(block)
		if match == nil {
			return ""
		}
		return Tidy(stripTags(match[1]))
	}
	city, district := field("city"), field("district")
	row := RawStore{
		ExternalID: field("id"),
		Name:       TidyName(StripPlaceCode(field("name"), city)),
		Address:    field("address"),
		City:       city,
		District:   district,
		Phone:      field("phone"),
		Website:    field("website"),
		Raw:        json.RawMessage(mustJSON(block)),
	}
	row.Latitude, row.Longitude = decimal(field("latitude")), decimal(field("longitude"))
	if row.Latitude == nil && row.Longitude == nil {
		row.Latitude, row.Longitude = pair(field("coordinates"))
	}
	return row
}

// stripTags leaves the text a reader would see. Markup inside a captured field is common --
// an address wrapped in a link, a name carrying a span -- and it is never part of the value.
var tags = regexp.MustCompile(`(?s)<[^>]*>`)

func stripTags(value string) string {
	return html.UnescapeString(tags.ReplaceAllString(value, " "))
}

func decimal(value string) *float64 {
	if value == "" {
		return nil
	}
	first, _ := pair(value + ",0")
	return first
}

func mustJSON(value string) []byte {
	encoded, e := json.Marshal(map[string]string{"html": strings.TrimSpace(value)})
	if e != nil {
		return []byte(`{}`)
	}
	return encoded
}
