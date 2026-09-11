// brand-logos collects each registered chain's own logo.
//
// A brand mark is public, it identifies the brand, and it does not change. So it is not
// user media: it needs no owner, no signed URL and no storage bill. This writes the files
// once, they are committed to the web application beside its other static assets, and a
// store of that brand shows its chain's mark the way Trustpilot does.
//
//	go run ./cmd/brand-logos -out ../ui/public/brands
//
// Fetching goes through the same robots-respecting fetcher an import uses.
//
// **Look at what it wrote before committing it.** A site's own markup is not always
// honest about which image is its mark: English Home's first image whose path says "logo"
// is a photograph of a phone. The size and shape tests below throw out favicons and
// banners, and a wrong-but-plausible picture still gets through. A brand with no usable
// mark is left without one, and its stores show their initial, which is correct.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/catalog"
	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
)

// Where a site says its own mark is, most trustworthy first. An apple-touch-icon is
// deliberately square and large; og:image is often a marketing banner rather than a mark,
// so it comes last.
// Where a site says its own mark is, most trustworthy first. An apple-touch-icon is
// deliberately square and large; og:image is often a marketing banner rather than a mark,
// so it comes last.
//
// Every attribute here tolerates its value being unquoted. Minified markup drops the quotes
// -- Merinos serves its whole page that way -- and a pattern that insists on them reports
// "no usable mark" for a page whose mark is plainly in the source.
var logoPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)<link[^>]+rel=` + attr(`[^"'>\s]*apple-touch-icon[^"'>\s]*`) + `[^>]*href=` + capture),
	regexp.MustCompile(`(?is)<link[^>]+href=` + capture + `[^>]*rel=` + attr(`[^"'>\s]*apple-touch-icon[^"'>\s]*`)),
	regexp.MustCompile(`(?is)<link[^>]+rel=` + attr(`[^"'>\s]*icon[^"'>\s]*`) + `[^>]*href=` + attr(`([^"'>\s]+\.svg[^"'>\s]*)`)),
	regexp.MustCompile(`(?is)<meta[^>]+property=` + attr(`og:image`) + `[^>]*content=` + capture),
	regexp.MustCompile(`(?is)<link[^>]+rel=` + attr(`[^"'>\s]*icon[^"'>\s]*`) + `[^>]*href=` + capture),
	// The mark in the masthead, last: a page holds many images whose path says "logo" and
	// only some of them are the brand. English Home's first such image is a photograph of
	// a phone, which is how this tool came to need an eye on its output.
	regexp.MustCompile(`(?is)<img[^>]+alt=` + attr(`[^"'>]*logo[^"'>]*`) + `[^>]*(?:src|data-src)=` + capture),
	regexp.MustCompile(`(?is)<img[^>]+(?:src|data-src)=` + attr(`([^"'>\s]*logo[^"'>\s]*)`)),
}

// attr wraps an attribute value so the pattern matches it quoted with either quote or not
// quoted at all.
func attr(value string) string { return `(?:"` + value + `"|'` + value + `'|` + value + `)` }

// capture is attr for the one value each pattern returns.
const capture = `(?:"([^"]+)"|'([^']+)'|([^"'>\s]+))`

// What separates a mark from a favicon is size, and what a store card needs is enough
// pixels along the longest edge to draw at 48 points. Requiring both edges to clear a
// threshold was the mistake: a wordmark is short. Doğtaş publishes its mark at 156x52 and
// Bellona at 468x72, and both were thrown away for being 52 and 72 pixels tall -- between
// them nine hundred shops showed an initial instead of their sign.
const (
	minLongestEdge  = 120
	minShortestEdge = 28
)

// The ratio cap was written to reject hero images and never could: a hero is typically
// three to one, the same shape as half the wordmarks in this trade. It is kept only to
// throw out the genuinely absurd -- a full-width page banner -- and set where a wordmark
// fits: Bellona's is 6.5 to one.
const maxLogoRatio = 8.0

var extensions = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp",
	"image/svg+xml": ".svg", "image/x-icon": ".ico", "image/vnd.microsoft.icon": ".ico",
}

func main() {
	out := flag.String("out", "../ui/public/brands", "directory to write the logos into")
	only := flag.String("source", "", "one brand slug, or empty for all")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	cfg, e := config.Load()
	if e != nil {
		log.Fatal(e)
	}
	db, e := database.Open(ctx, cfg.DatabaseURL)
	if e != nil {
		log.Fatal(e)
	}
	defer db.Close()
	brands, e := catalog.Brands(ctx, db, *only)
	if e != nil {
		log.Fatal(e)
	}
	if e = os.MkdirAll(*out, 0o755); e != nil {
		log.Fatal(e)
	}

	fetcher := catalog.NewFetcher()
	var found, missing []string
	for _, brand := range brands {
		if strings.TrimSpace(brand.Website) == "" {
			missing = append(missing, brand.Slug+" (no website)")
			continue
		}
		// The home page first, then the page whose address we already know works: a chain's
		// store locator carries the same masthead, and for Merinos the mark is on that page
		// and nowhere on the home page at all. Trying it costs one request we have already
		// proven we can make.
		var name string
		var e error
		for _, page := range []string{brand.Website, locatorPage(brand)} {
			if strings.TrimSpace(page) == "" {
				continue
			}
			if name, e = save(ctx, fetcher, page, filepath.Join(*out, brand.Slug)); e == nil {
				break
			}
		}
		if name == "" {
			missing = append(missing, fmt.Sprintf("%s (%v)", brand.Slug, e))
			continue
		}
		found = append(found, fmt.Sprintf("%-20s %s", brand.Slug, name))
	}
	sort.Strings(found)
	for _, line := range found {
		fmt.Println(" ", line)
	}
	// The extension differs per brand -- svg here, png there -- so the file names are
	// written down rather than guessed at by whatever renders them.
	if e := writeManifest(*out); e != nil {
		log.Fatal(e)
	}
	fmt.Printf("%d logos written, %d missing\n", len(found), len(missing))
	for _, line := range missing {
		fmt.Println("  missing:", line)
	}
}

// writeManifest lists what is actually on disk, so a logo deleted by hand because it
// turned out to be a banner disappears from the product too.
func writeManifest(dir string) error {
	entries, e := os.ReadDir(dir)
	if e != nil {
		return e
	}
	logos := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == manifestName {
			continue
		}
		slug := strings.TrimSuffix(name, filepath.Ext(name))
		logos[slug] = name
	}
	body, e := json.MarshalIndent(logos, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(filepath.Join(dir, manifestName), append(body, '\n'), 0o644)
}

const manifestName = "manifest.json"

func save(ctx context.Context, fetcher *catalog.Fetcher, website, base string) (string, error) {
	page, e := fetcher.Get(ctx, website)
	if e != nil {
		return "", e
	}
	html := string(page)
	// A chain that draws its wordmark directly in the page has no file to fetch. English
	// Home is one, and İstikbal and Taç turned out to be others; between them the collector
	// reported "no usable mark" for sites whose mark was right there in the markup.
	if svg := mastheadSVG(html); svg != "" {
		if e = os.WriteFile(base+".svg", []byte(svg), 0o644); e != nil {
			return "", e
		}
		return fmt.Sprintf("%-6s %5d KB  inline in the page", ".svg", len(svg)/1024), nil
	}
	// Every candidate of every pattern, in order, rather than the first of each. A page
	// offers several images whose path says "logo" and the first is routinely the wrong one
	// -- a vendor's badge, an app-store button -- and stopping at it threw away the real
	// mark sitting two matches later.
	var candidates []string
	for _, pattern := range logoPatterns {
		for _, match := range pattern.FindAllStringSubmatch(html, -1) {
			candidates = append(candidates, firstGroup(match))
		}
	}
	for _, candidate := range candidates {
		target := absolute(website, strings.TrimSpace(candidate))
		if target == "" {
			continue
		}
		body, e := fetcher.Get(ctx, target)
		if e != nil || len(body) < 256 {
			continue
		}
		extension := extensions[http.DetectContentType(body)]
		if strings.HasSuffix(strings.ToLower(target), ".svg") {
			extension = ".svg"
		}
		if extension == "" || extension == ".ico" {
			// A favicon is 16 pixels of nothing at the size a store card draws. Keep
			// looking rather than shipping a blur.
			continue
		}
		// Measured, not trusted: plenty of sites serve a 32-pixel favicon from a path
		// called apple-touch-icon, and the first pass of this tool shipped twenty of them.
		// A vector needs no measuring.
		if extension == ".webp" {
			// No decoder for webp in the standard library, so size stands in for
			// dimensions: a 32-pixel icon does not reach three kilobytes.
			if len(body) < 3072 {
				continue
			}
		} else if extension != ".svg" {
			config, _, e := image.DecodeConfig(bytes.NewReader(body))
			if e != nil {
				continue
			}
			longest, shortest := config.Width, config.Height
			if longest < shortest {
				longest, shortest = shortest, longest
			}
			if longest < minLongestEdge || shortest < minShortestEdge {
				continue
			}
			// Wider than this is a page banner, not a sign.
			if float64(longest)/float64(shortest) > maxLogoRatio {
				continue
			}
		}
		if e = os.WriteFile(base+extension, body, 0o644); e != nil {
			return "", e
		}
		return fmt.Sprintf("%-6s %5d KB  %s", extension, len(body)/1024, target), nil
	}
	return "", fmt.Errorf("no usable mark on the page")
}

func absolute(page, raw string) string {
	switch {
	case strings.HasPrefix(raw, "//"):
		return "https:" + raw
	case strings.HasPrefix(raw, "http://"), strings.HasPrefix(raw, "https://"):
		return raw
	case strings.HasPrefix(raw, "/"):
		trimmed := strings.TrimSuffix(page, "/")
		if index := strings.Index(trimmed[8:], "/"); index >= 0 {
			trimmed = trimmed[:8+index]
		}
		return trimmed + raw
	}
	return ""
}

// mastheadSVG returns the first inline <svg> in the top of the document, when there is one
// and it is small enough to be a mark rather than an illustration.
//
// Only the head of the page is searched: a masthead is at the top, and further down every
// icon on the page is an <svg> too. The size bounds are what separate a wordmark from a
// decorative drawing -- under a quarter of a kilobyte is an arrow, over forty is a scene.
func mastheadSVG(html string) string {
	head := html
	if len(head) > mastheadBytes {
		head = head[:mastheadBytes]
	}
	best, bestEdge := "", 0.0
	for _, match := range inlineSVG.FindAllString(head, -1) {
		if len(match) < minInlineSVG || len(match) > maxInlineSVG {
			continue
		}
		// The drawing's own declared size is what separates a mark from an arrow. Byte
		// length does not: the first pass of this took a 17-pixel chevron from five
		// different chains because their markup happened to weigh a kilobyte.
		longest, shortest := svgSize(match)
		if longest < minInlineEdge || shortest < minInlineShortEdge || longest/shortest > maxLogoRatio {
			continue
		}
		if longest > bestEdge {
			best, bestEdge = match, longest
		}
	}
	return best
}

// svgSize reads the drawing's own dimensions, from its viewBox where it has one and from
// its width and height otherwise.
func svgSize(svg string) (longest, shortest float64) {
	var w, h float64
	if box := viewBox.FindStringSubmatch(svg); box != nil {
		w, h = number(box[3]), number(box[4])
	}
	if w == 0 || h == 0 {
		if m := svgWidth.FindStringSubmatch(svg); m != nil {
			w = number(m[1])
		}
		if m := svgHeight.FindStringSubmatch(svg); m != nil {
			h = number(m[1])
		}
	}
	if w == 0 || h == 0 {
		return 0, 0
	}
	if w < h {
		return h, w
	}
	return w, h
}

func number(v string) float64 {
	parsed, e := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if e != nil {
		return 0
	}
	return parsed
}

var (
	viewBox   = regexp.MustCompile(`(?i)viewBox=["']\s*([0-9.eE+-]+)[ ,]+([0-9.eE+-]+)[ ,]+([0-9.eE+-]+)[ ,]+([0-9.eE+-]+)`)
	svgWidth  = regexp.MustCompile(`(?i)<svg[^>]*\bwidth=["']?([0-9.]+)`)
	svgHeight = regexp.MustCompile(`(?i)<svg[^>]*\bheight=["']?([0-9.]+)`)
)

var inlineSVG = regexp.MustCompile(`(?is)<svg[^>]*>.*?</svg>`)

const (
	mastheadBytes = 120 << 10
	minInlineSVG  = 256
	maxInlineSVG  = 40 << 10
	// A mark is drawn at something like a wordmark's proportions; an interface icon is a
	// small square. English Home's is 244 by 33.
	minInlineEdge      = 60
	minInlineShortEdge = 8
)

// locatorPage is the address this brand's store list is read from, which is a page of the
// brand's own site and therefore carries the brand's own masthead.
func locatorPage(brand catalog.BrandSpec) string {
	var config struct {
		URL  string   `json:"url"`
		URLs []string `json:"urls"`
	}
	if len(brand.LocatorConfig) == 0 {
		return ""
	}
	if json.Unmarshal(brand.LocatorConfig, &config) != nil {
		return ""
	}
	if config.URL != "" {
		return config.URL
	}
	if len(config.URLs) > 0 {
		return config.URLs[0]
	}
	return ""
}

// firstGroup returns whichever of a pattern's alternative captures actually matched: the
// value may have been double-quoted, single-quoted or bare.
func firstGroup(match []string) string {
	for _, group := range match[1:] {
		if group != "" {
			return group
		}
	}
	return ""
}
