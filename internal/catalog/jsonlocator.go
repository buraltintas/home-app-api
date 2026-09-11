package catalog

import (
	"context"
	"encoding/json"
	"fmt"
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
	fetcher *Fetcher
}

// JSONLocatorConfig is what the registry stores for such a brand. The field names are the
// publisher's own, in the publisher's own language; mapping them here is the whole job.
type JSONLocatorConfig struct {
	// URL is fetched once per page. When Pages is set, "{page}" in the URL is replaced.
	URL   string `json:"url"`
	Pages int    `json:"pages"`
	// List is the dotted path to the array of stores, empty when the body is the array.
	List string `json:"list"`
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
	// NamePrefix is prepended when a chain publishes branch names without its own name --
	// "ANK ACITY AVM" is not a shop sign anyone would recognise on its own.
	NamePrefix string `json:"name_prefix"`
}

func NewJSONLocator(spec BrandSpec, fetcher *Fetcher) (*JSONLocator, error) {
	var config JSONLocatorConfig
	if len(spec.LocatorConfig) > 0 {
		if e := json.Unmarshal(spec.LocatorConfig, &config); e != nil {
			return nil, fmt.Errorf("%s locator config: %w", spec.Slug, e)
		}
	}
	if config.URL == "" {
		return nil, fmt.Errorf("%s has no locator url", spec.Slug)
	}
	return &JSONLocator{spec: spec, config: config, fetcher: fetcher}, nil
}

func (l *JSONLocator) Brand() BrandSpec { return l.spec }

func (l *JSONLocator) Fetch(ctx context.Context) ([]RawStore, error) {
	pages := l.config.Pages
	if pages < 1 {
		pages = 1
	}
	var out []RawStore
	for page := 1; page <= pages; page++ {
		url := strings.ReplaceAll(l.config.URL, "{page}", strconv.Itoa(page))
		body, e := l.fetcher.Get(ctx, url)
		if e != nil {
			return nil, e
		}
		var document any
		if e = json.Unmarshal(body, &document); e != nil {
			return nil, fmt.Errorf("%s page %d: %w", l.spec.Slug, page, e)
		}
		items, e := listAt(document, l.config.List)
		if e != nil {
			return nil, fmt.Errorf("%s page %d: %w", l.spec.Slug, page, e)
		}
		// A page that answers with nothing is the end of the list, not a failure; several
		// locators keep answering 200 past their last page.
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			object, ok := item.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, l.mapRow(object))
		}
	}
	return out, nil
}

func (l *JSONLocator) mapRow(object map[string]any) RawStore {
	raw, _ := json.Marshal(object)
	name := text(object, l.config.Name)
	if l.config.NamePrefix != "" {
		name = strings.TrimSpace(l.config.NamePrefix + " " + name)
	}
	return RawStore{
		ExternalID: text(object, l.config.ID),
		Name:       name,
		Address:    text(object, l.config.Address),
		City:       text(object, l.config.City),
		District:   text(object, l.config.District),
		Phone:      text(object, l.config.Phone),
		Website:    text(object, l.config.Website),
		ImageURL:   text(object, l.config.Image),
		Latitude:   number(object, l.config.Latitude),
		Longitude:  number(object, l.config.Longitude),
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
	items, ok := current.([]any)
	if !ok {
		return nil, fmt.Errorf("%q is not a list", path)
	}
	return items, nil
}

// text reads a field that may be published as a string or as a number, because plenty of
// locators publish a store id as an integer and a telephone number as a string.
func text(object map[string]any, field string) string {
	if field == "" {
		return ""
	}
	switch value := object[field].(type) {
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
func number(object map[string]any, field string) *float64 {
	if field == "" {
		return nil
	}
	var parsed float64
	switch value := object[field].(type) {
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
