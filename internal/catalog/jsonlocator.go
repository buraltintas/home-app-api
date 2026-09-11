package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// JSONLocator reads a store list that a brand already publishes as JSON, which most of them
// do: the page a customer sees is usually a thin wrapper around an endpoint their own
// front-end calls. Nothing here is specific to a chain, so adding one of these brands is a
// row in the registry rather than a file of Go.
//
// Verified against English Home, whose locator answers with
// {"magazalar":[{"id":…,"tanim":…,"adres":…,"il":…,"ilce":…,"latitude":…,"longitude":…}]}
// -- every field this catalogue needs, already structured.
type JSONLocator struct {
	spec    BrandSpec
	config  JSONLocatorConfig
	extract *regexp.Regexp
	require *regexp.Regexp
	fetcher *Fetcher
}

// JSONLocatorConfig is what the registry stores for such a brand. The field names are the
// publisher's own, in the publisher's own language; mapping them here is the whole job.
type JSONLocatorConfig struct {
	// URL is fetched once per page. When Pages is set, "{page}" in the URL is replaced.
	//
	// "{province}" is replaced with each of Turkey's 81 province codes, "1" through "81",
	// one request each. Unpadded, which is what these endpoints want: the store pages
	// themselves strip the leading zero before asking, and a padded "07" answers with an
	// empty list rather than an error -- so Antalya and eight other provinces come back
	// silently empty if this is got wrong. A chain whose locator answers per province rather than all at once
	// is common enough here that it belongs in the shared adapter: the customer picks a
	// province from a dropdown and the page asks for that province, so the whole country
	// is eighty-one of the same request.
	URL string `json:"url"`
	// URLs is the alternative to URL for a publisher that splits its list across more than
	// one address. Every one is fetched and the results are pooled; a shop appearing in two
	// of them is deduplicated by its identifier like any other repeat.
	URLs []string `json:"urls"`
	// Method and Body carry a locator that answers only to a POST. A WordPress site puts
	// every one of its endpoints behind one address and tells them apart by a form field,
	// so there is nothing to fetch without a body.
	Method string `json:"method"`
	Body   string `json:"body"`
	// Bodies pairs with URLs when a publisher splits its list by a form field rather than by
	// address: the same endpoint asked several different questions.
	Bodies []string `json:"bodies"`
	// Over makes this a two-step locator: fetch that list first, then substitute each of
	// its values for "{over}" in this one. "Ask which cities exist, then ask for each
	// city's shops" is how a great many store finders are built, and a static list of
	// identifiers would be wrong the first time the publisher added a city.
	Over *struct {
		URL    string `json:"url"`
		Method string `json:"method"`
		Body   string `json:"body"`
		List   string `json:"list"`
		Field  string `json:"field"`
		// Label is the readable half of whatever Field identifies -- the province's name
		// beside its id. A publisher that answers per province often leaves the province
		// off the rows themselves, having just been asked for it; the question we asked is
		// part of the answer, and this carries it back onto the row.
		Label string `json:"label"`
	} `json:"over"`
	Pages int `json:"pages"`
	// Extract pulls the store data out of a page that is not itself JSON. Plenty of chains
	// render their list into the HTML rather than serving it from an endpoint -- there is
	// nothing to call, and the data is right there in the markup.
	//
	// It is a regular expression with one capturing group. Every match contributes: a page
	// that carries one object per shop (Karaca puts each in its own hidden div) yields as
	// many rows as it has shops, and a page carrying the whole list in one array yields
	// that array. Either way what comes out is parsed as JSON and mapped by the fields
	// below, so a page-rendered locator costs a line of configuration rather than a second
	// adapter.
	Extract string `json:"extract"`
	// ExtractAfter is the other shape the same problem takes: a page that carries its whole
	// list as one JSON value, nested inside a larger blob of state. A regular expression
	// cannot cut that out -- the value contains brackets of its own and RE2 will not count
	// them -- so this names a literal marker instead ("\"items\":"), and the adapter reads
	// one balanced value starting at the bracket after it.
	ExtractAfter string `json:"extract_after"`
	// List is the dotted path to the array of stores, empty when the body is the array.
	List string `json:"list"`
	// Require keeps only the rows whose name matches, for a publisher that answers with
	// more than one chain's shops in a single list. Yataş publishes its own and Enza Home's
	// together, marking each row with the chain's code, which is the publisher telling us
	// which is which -- better evidence than anything we could infer.
	Require string `json:"require"`
	// Field names inside one store object.
	ID        string `json:"id"`
	Name      string `json:"name"`
	Address   string `json:"address"`
	City      string `json:"city"`
	District  string `json:"district"`
	Phone     string `json:"phone"`
	Website   string `json:"website"`
	Image     string `json:"image"`
	Latitude  string `json:"latitude"`
	Longitude string `json:"longitude"`
	// Coordinates is the alternative to the pair above, for the many locators that publish
	// one string -- "40.99736,28.87229", or the contents of a maps link. Whichever the
	// publisher chose, the row that reaches the catalogue is the same.
	Coordinates string `json:"coordinates"`
}

func NewJSONLocator(spec BrandSpec, fetcher *Fetcher) (*JSONLocator, error) {
	var config JSONLocatorConfig
	if len(spec.LocatorConfig) > 0 {
		if e := json.Unmarshal(spec.LocatorConfig, &config); e != nil {
			return nil, fmt.Errorf("%s locator config: %w", spec.Slug, e)
		}
	}
	if config.URL == "" && len(config.URLs) == 0 {
		return nil, fmt.Errorf("%s has no locator url", spec.Slug)
	}
	locator := &JSONLocator{spec: spec, config: config, fetcher: fetcher}
	if config.Extract != "" {
		compiled, e := regexp.Compile(config.Extract)
		if e != nil {
			return nil, fmt.Errorf("%s extract pattern: %w", spec.Slug, e)
		}
		if compiled.NumSubexp() != 1 {
			return nil, fmt.Errorf("%s extract pattern needs exactly one capturing group", spec.Slug)
		}
		locator.extract = compiled
	}
	if config.Require != "" {
		compiled, e := regexp.Compile(config.Require)
		if e != nil {
			return nil, fmt.Errorf("%s require pattern: %w", spec.Slug, e)
		}
		locator.require = compiled
	}
	return locator, nil
}

func (l *JSONLocator) Brand() BrandSpec { return l.spec }

func (l *JSONLocator) Fetch(ctx context.Context) ([]RawStore, error) {
	// A request is its address and its body together. Both carry the substitutions, because
	// a POST locator puts in its body exactly what a GET one puts in its query string --
	// and substituting only the address leaves the body asking for whatever the publisher
	// defaults to, which looks like a working import returning the same row 76 times.
	requests, e := l.expand(ctx, l.requests())
	if e != nil {
		return nil, e
	}
	var out []RawStore
	var missing int
	var last error
	fanned := len(requests) > 1 || strings.Contains(requests[0].url+requests[0].body, "{province}")
	for _, request := range requests {
		for _, province := range l.provinces(request.url + request.body) {
			rows, e := l.fetchPages(ctx, request.fill("{province}", province))
			if e != nil {
				// One of many questions answering "not found" is a province or a city this
				// chain does not serve, not a broken locator: Dinarsu's own site answers 404
				// for some of the city identifiers it publishes. Abandoning the brand there
				// loses the other eighty. A locator with a single address is different --
				// a 404 on it means the locator has moved.
				if fanned && errors.Is(e, errNotFound) {
					missing++
					last = e
					continue
				}
				return nil, e
			}
			out = append(out, rows...)
		}
	}
	// Unless every one of them was missing, which is a moved locator wearing a fan-out.
	if len(out) == 0 && missing > 0 {
		return nil, last
	}
	return out, nil
}

// request is one address, the body sent with it, and what that question was about.
type request struct{ url, body, label string }

func (r request) fill(token, value string) request {
	return request{strings.ReplaceAll(r.url, token, value), strings.ReplaceAll(r.body, token, value), r.label}
}

// expand resolves "{over}" by asking the publisher what values exist.
func (l *JSONLocator) expand(ctx context.Context, requests []request) ([]request, error) {
	if l.config.Over == nil {
		return requests, nil
	}
	over := l.config.Over
	body, e := l.fetcher.Send(ctx, over.Method, over.URL, over.Body)
	if e != nil {
		return nil, fmt.Errorf("%s: listing what to iterate: %w", l.spec.Slug, e)
	}
	var document any
	if e = json.Unmarshal(body, &document); e != nil {
		return nil, fmt.Errorf("%s: listing what to iterate: %w", l.spec.Slug, e)
	}
	items, e := listAt(document, over.List)
	if e != nil {
		return nil, fmt.Errorf("%s: listing what to iterate: %w", l.spec.Slug, e)
	}
	type step struct{ value, label string }
	var values []step
	seen := map[string]bool{}
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		value := text(object, over.Field)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		values = append(values, step{value, text(object, over.Label)})
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("%s: nothing to iterate over", l.spec.Slug)
	}
	out := make([]request, 0, len(requests)*len(values))
	for _, base := range requests {
		for _, step := range values {
			asked := base.fill("{over}", step.value)
			asked.label = step.label
			out = append(out, asked)
		}
	}
	return out, nil
}

// requests is the address-and-body pairs this locator starts from. Several addresses share
// one body; several bodies for one address are written as that address repeated, which is
// how a publisher that splits its list by a form field (Dinarsu, by shop type) is described.
func (l *JSONLocator) requests() []request {
	addresses := l.config.URLs
	if len(addresses) == 0 {
		addresses = []string{l.config.URL}
	}
	bodies := l.config.Bodies
	if len(bodies) == 0 {
		bodies = []string{l.config.Body}
	}
	var out []request
	for index, address := range addresses {
		body := bodies[0]
		if len(bodies) == len(addresses) {
			body = bodies[index]
		}
		out = append(out, request{url: address, body: body})
	}
	return out
}

// provinces is the list of substitutions for "{province}", or a single empty one when the
// URL does not ask for it.
func (l *JSONLocator) provinces(address string) []string {
	if !strings.Contains(address, "{province}") {
		return []string{""}
	}
	out := make([]string, 0, 81)
	for code := 1; code <= 81; code++ {
		out = append(out, strconv.Itoa(code))
	}
	return out
}

func (l *JSONLocator) fetchPages(ctx context.Context, base request) ([]RawStore, error) {
	pages := l.config.Pages
	if pages < 1 {
		pages = 1
	}
	var out []RawStore
	for page := 1; page <= pages; page++ {
		paged := base.fill("{page}", strconv.Itoa(page))
		body, e := l.fetcher.Send(ctx, l.config.Method, paged.url, paged.body)
		if e != nil {
			// The body is part of the request for a POST locator, so an error that names
			// only the address says nothing about which question failed.
			if paged.body != "" {
				return nil, fmt.Errorf("%s [%s]: %w", l.spec.Slug, paged.body, e)
			}
			return nil, e
		}
		document, e := l.document(body)
		if e != nil {
			return nil, fmt.Errorf("%s page %d: %w", l.spec.Slug, page, e)
		}
		items, e := listAt(document, l.config.List)
		if e != nil {
			return nil, fmt.Errorf("%s page %d: %w", l.spec.Slug, page, e)
		}
		// A page that answers with nothing is the end of the list, not a failure; several
		// locators keep answering 200 past their last page. A province with no branch
		// answers the same way, and simply contributes nothing.
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			object, ok := item.(map[string]any)
			if !ok {
				continue
			}
			// Tested against the name as published, before any tidying: the marker is the
			// publisher's own code and tidying is free to recase or drop it.
			if l.require != nil && !l.require.MatchString(text(object, l.config.Name)) {
				continue
			}
			row := l.mapRow(object)
			// The province we asked about, when the rows themselves do not carry one.
			// Dinarsu answers per province and leaves every row's province empty; without
			// this its 1,650 dealers are placeless, and two dealers of the same name in
			// two provinces are indistinguishable.
			if row.City == "" {
				row.City = base.label
			}
			out = append(out, row)
		}
	}
	return out, nil
}

// document turns a fetched body into something listAt can walk: the body itself when the
// endpoint answers with JSON, or whatever Extract finds when it does not.
func (l *JSONLocator) document(body []byte) (any, error) {
	if l.config.ExtractAfter != "" {
		value, e := balancedAfter(body, l.config.ExtractAfter)
		if e != nil {
			return nil, e
		}
		var out any
		return out, json.Unmarshal(value, &out)
	}
	if l.extract == nil {
		var out any
		return out, json.Unmarshal(body, &out)
	}
	matches := l.extract.FindAllSubmatch(unprotectEmails(body), -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("nothing matched the extract pattern")
	}
	// One match is taken as the whole published list, exactly as an endpoint's body would
	// be, so `list` can point into it. Several are the shops themselves.
	if len(matches) == 1 {
		var out any
		return out, json.Unmarshal(matches[0][1], &out)
	}
	out := make([]any, 0, len(matches))
	for _, match := range matches {
		var item any
		if e := json.Unmarshal(match[1], &item); e != nil {
			// One unreadable block is one shop missing, not a failed import; a page holds
			// markup we did not anticipate far more often than an endpoint does.
			continue
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%d block(s) matched the extract pattern and none held JSON", len(matches))
	}
	return out, nil
}

// balancedAfter returns the one JSON array or object that begins after marker, counting
// brackets so that the value's own nesting does not end it early. Strings are tracked,
// because a bracket inside a shop's address is not structure.
func balancedAfter(body []byte, marker string) ([]byte, error) {
	at := bytes.Index(body, []byte(marker))
	if at < 0 {
		return nil, fmt.Errorf("the page does not contain %q", marker)
	}
	start := at + len(marker)
	for start < len(body) && body[start] != '[' && body[start] != '{' {
		start++
	}
	if start == len(body) {
		return nil, fmt.Errorf("nothing follows %q that could be JSON", marker)
	}
	open, shut := body[start], byte(']')
	if open == '{' {
		shut = '}'
	}
	depth, inString, escaped := 0, false, false
	for at := start; at < len(body); at++ {
		c := body[at]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
		case c == open:
			depth++
		case c == shut:
			depth--
			if depth == 0 {
				return body[start : at+1], nil
			}
		}
	}
	return nil, fmt.Errorf("the value after %q is never closed", marker)
}

// cloudflareEmail matches the markup Cloudflare's email obfuscation leaves behind. It
// rewrites addresses everywhere in a document, including inside JSON a page had already
// serialised into itself, and the unescaped quotes in the anchor it substitutes make that
// JSON unparseable -- Karaca's entire store list fails on its first shop's mail field. The
// anchor is removed before extraction. This is a fact about Cloudflare, not about any one
// chain, and holds for every site behind it.
var cloudflareEmail = regexp.MustCompile(`(?s)<a[^>]*class="__cf_email__"[^>]*>.*?</a>`)

func unprotectEmails(body []byte) []byte { return cloudflareEmail.ReplaceAll(body, nil) }

func (l *JSONLocator) mapRow(object map[string]any) RawStore {
	raw, _ := json.Marshal(object)
	// The chain's internal city code goes first, then the casing, then the chain's name --
	// in that order. Cased before the prefix is added rather than after, because a shouted
	// branch name with a styled prefix in front of it reads as mixed case, and the caser
	// then leaves the whole thing alone: "Madame Coco ADANA CEYHAN CADDE".
	city, district := text(object, l.config.City), text(object, l.config.District)
	// The publisher's own name, untouched apart from whitespace. Everything done to it --
	// repairing its capital I's, removing the chain's internal city code, casing it, adding
	// the town and the chain -- happens in the importer, in that order, because each step
	// needs what the one before it produced and the first two need the name still shouted.
	name := Tidy(text(object, l.config.Name))
	latitude, longitude := number(object, l.config.Latitude), number(object, l.config.Longitude)
	if latitude == nil && longitude == nil {
		latitude, longitude = pair(text(object, l.config.Coordinates))
	}
	return RawStore{
		ExternalID: text(object, l.config.ID),
		Name:       name,
		Address:    text(object, l.config.Address),
		City:       city,
		District:   district,
		Phone:      text(object, l.config.Phone),
		Website:    text(object, l.config.Website),
		ImageURL:   text(object, l.config.Image),
		Latitude:   latitude,
		Longitude:  longitude,
		Raw:        raw,
	}
}

// listAt walks a dotted path to the array of stores.
func listAt(document any, path string) ([]any, error) {
	current := document
	if path != "" {
		for _, step := range strings.Split(path, ".") {
			object, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("no object at %q", step)
			}
			current, ok = object[step]
			if !ok {
				return nil, fmt.Errorf("no field %q in the response", step)
			}
		}
	}
	switch collection := current.(type) {
	case []any:
		return collection, nil
	case map[string]any:
		// Some publishers hand back an object keyed "0", "1", "2" where any other would
		// have used an array -- PHP does this whenever a list has been through a map. It is
		// a list; the keys carry nothing. Sorted, so the order is the publisher's own and
		// not Go's map iteration, which would shuffle a dry run's output between runs.
		keys := make([]string, 0, len(collection))
		for key := range collection {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		items := make([]any, 0, len(collection))
		for _, key := range keys {
			items = append(items, collection[key])
		}
		return items, nil
	}
	return nil, fmt.Errorf("%q is neither a list nor an object", path)
}

// field walks a dotted path into one store object. Publishers nest: Madame Coco's township
// is an object holding both the district's name and, under it, the city's -- and a mapping
// that could only read flat fields silently read nothing and left the row placeless.
func field(object map[string]any, path string) any {
	if path == "" {
		return nil
	}
	var current any = object
	for _, step := range strings.Split(path, ".") {
		node, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = node[step]
	}
	return current
}

// text reads a field that may be published as a string or as a number, because plenty of
// locators publish a store id as an integer and a telephone number as a string.
func text(object map[string]any, path string) string {
	if path == "" {
		return ""
	}
	switch value := field(object, path).(type) {
	case string:
		return strings.TrimSpace(value)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(value)
	}
	return ""
}

// number reads a coordinate published either as a number or as a string, and refuses one
// that is not a coordinate at all. A missing point is handled; a wrong one is not.
func number(object map[string]any, path string) *float64 {
	if path == "" {
		return nil
	}
	var parsed float64
	switch value := field(object, path).(type) {
	case float64:
		parsed = value
	case string:
		trimmed := strings.TrimSpace(strings.ReplaceAll(value, ",", "."))
		if trimmed == "" {
			return nil
		}
		converted, e := strconv.ParseFloat(trimmed, 64)
		if e != nil {
			return nil
		}
		parsed = converted
	default:
		return nil
	}
	if parsed == 0 {
		return nil
	}
	return &parsed
}

// pair reads a coordinate published as one string: "40.99736, 28.87229", latitude first,
// which is the order every mapping service writes and every locator that does this copies.
// Anything that is not two numbers is not a coordinate, and says so by returning nothing.
func pair(value string) (*float64, *float64) {
	first, second, found := strings.Cut(strings.TrimSpace(value), ",")
	if !found {
		return nil, nil
	}
	latitude, e := strconv.ParseFloat(strings.TrimSpace(first), 64)
	if e != nil {
		return nil, nil
	}
	longitude, e := strconv.ParseFloat(strings.TrimSpace(second), 64)
	if e != nil {
		return nil, nil
	}
	return &latitude, &longitude
}
