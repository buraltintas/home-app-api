package catalog

import (
	"bufio"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

//go:embed data/harvest/*.tsv
var harvested embed.FS

// The brands whose own store finder a plain HTTP client cannot read.
//
// Their `robots.txt` permits these pages -- the one that was checked disallows carts,
// accounts and password resets, and nothing about dealers -- but the CDN in front of the
// site answers anything that is not a browser with 403 on every path. That is a bot filter
// rather than a policy, and the difference matters: for months this was recorded the other
// way round, as the brands refusing us, and five of the largest dealer networks in the
// country stayed out of the catalogue on the strength of a misread status code.
//
// So the page is read in a browser, which is what the page is for, and what it gave up is
// committed here as a file with the date on it. Nothing pretends to be a browser and
// nothing works around a refusal; the cost is that this file does not refresh itself, and
// a brand read this way cannot join the monthly unattended run until it can be fetched
// plainly. The date in the header is how anybody tells how stale it is.
type harvestSource struct {
	spec BrandSpec
	file string
}

func NewHarvestSource(spec BrandSpec) (Source, error) {
	var config struct {
		File string `json:"file"`
	}
	if len(spec.LocatorConfig) > 0 {
		if e := json.Unmarshal(spec.LocatorConfig, &config); e != nil {
			return nil, fmt.Errorf("%s: %w", spec.Slug, e)
		}
	}
	if strings.TrimSpace(config.File) == "" {
		return nil, fmt.Errorf("%s: harvest locator needs a file", spec.Slug)
	}
	return &harvestSource{spec: spec, file: "data/harvest/" + strings.TrimSpace(config.File)}, nil
}

func (s *harvestSource) Brand() BrandSpec { return s.spec }

func (s *harvestSource) Fetch(ctx context.Context) ([]RawStore, error) {
	body, e := harvested.ReadFile(s.file)
	if e != nil {
		return nil, fmt.Errorf("%s: %w", s.spec.Slug, e)
	}
	var out []RawStore
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		name, address := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if name == "" || address == "" {
			continue
		}
		phone := ""
		if len(parts) > 2 {
			phone = strings.TrimSpace(parts[2])
		}
		// The brand places every one of its dealers itself, so nothing here is guessed from
		// an address. A row without a pair is left unplaced rather than dropped on its
		// district's centre.
		var latitude, longitude *float64
		if len(parts) > 4 {
			if lat, e := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64); e == nil {
				if lon, e := strconv.ParseFloat(strings.TrimSpace(parts[4]), 64); e == nil {
					latitude, longitude = &lat, &lon
				}
			}
		}
		city, district := placeFromAddress(address)
		// The brand publishes no id of its own here, so one is derived from the two fields
		// that identify a shop. It has to be stable across reads or every refresh would
		// insert the whole network again; name and address together are what the brand
		// itself uses to tell one dealer from the next.
		sum := sha256.Sum256([]byte(name + "|" + address))
		out = append(out, RawStore{
			ExternalID: hex.EncodeToString(sum[:12]),
			Name:       name,
			Address:    address,
			City:       city,
			District:   district,
			Phone:      phone,
			Latitude:   latitude,
			Longitude:  longitude,
		})
	}
	return out, scanner.Err()
}

// placeFromAddress reads the province and district off the end of a Turkish postal address,
// which is where several of these brands write them: "... NO:33 BİSMİL/DİYARBAKIR". Some
// rows carry the pair twice; the last one is taken, and a row that carries neither is left
// for the normaliser, which knows how to read a province out of the whole address and how
// to fall back on the shop's own point.
//
// A slash in a Turkish address is far more often a house number than a place -- "NO:32/1D",
// "No. 2/2" -- and scanning from the end finds whichever comes last. So a half carrying a
// digit disqualifies the pair: no province or district in the country has one in its name,
// and reading "2/2" as a place hands the normaliser two fields of rubbish to recover from.
func placeFromAddress(address string) (city, district string) {
	fields := strings.Fields(address)
	for i := len(fields) - 1; i >= 0; i-- {
		slash := strings.Index(fields[i], "/")
		if slash <= 0 || slash == len(fields[i])-1 {
			continue
		}
		left := strings.TrimSpace(fields[i][:slash])
		right := strings.TrimSpace(fields[i][slash+1:])
		if left == "" || right == "" || hasDigit(left) || hasDigit(right) {
			continue
		}
		return right, left
	}
	return "", ""
}

func hasDigit(text string) bool {
	return strings.ContainsAny(text, "0123456789")
}
