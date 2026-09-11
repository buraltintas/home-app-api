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
	"strings"
	"time"

	"github.com/burakaltintas/home-app-api/internal/catalog"
	"github.com/burakaltintas/home-app-api/internal/config"
	"github.com/burakaltintas/home-app-api/internal/database"
)

// Where a site says its own mark is, most trustworthy first. An apple-touch-icon is
// deliberately square and large; og:image is often a marketing banner rather than a mark,
// so it comes last.
var logoPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)<link[^>]+rel=["'][^"']*apple-touch-icon[^"']*["'][^>]*href=["']([^"']+)["']`),
	regexp.MustCompile(`(?is)<link[^>]+href=["']([^"']+)["'][^>]*rel=["'][^"']*apple-touch-icon[^"']*["']`),
	regexp.MustCompile(`(?is)<link[^>]+rel=["'][^"']*icon[^"']*["'][^>]*href=["']([^"']+\.svg[^"']*)["']`),
	regexp.MustCompile(`(?is)<meta[^>]+property=["']og:image["'][^>]*content=["']([^"']+)["']`),
	regexp.MustCompile(`(?is)<link[^>]+rel=["'][^"']*icon[^"']*["'][^>]*href=["']([^"']+)["']`),
	// The mark in the masthead, last: a page holds many images whose path says "logo" and
	// only some of them are the brand. English Home's first such image is a photograph of
	// a phone, which is how this tool came to need an eye on its output.
	regexp.MustCompile(`(?is)<img[^>]+alt=["'][^"']*logo[^"']*["'][^>]*(?:src|data-src)=["']([^"']+)["']`),
	regexp.MustCompile(`(?is)<img[^>]+(?:src|data-src)=["']([^"']*logo[^"']*)["']`),
}

// Below this a mark is a favicon, not a logo: a store card draws it at 48 points, and a
// 32-pixel image there is a smudge.
const minLogoPixels = 96

// A wordmark is wide; a banner is wider. Four to one keeps "MADAME COCO" and rejects a
// hero image.
const maxLogoRatio = 4.0

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
		name, e := save(ctx, fetcher, brand.Website, filepath.Join(*out, brand.Slug))
		if e != nil {
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
	for _, pattern := range logoPatterns {
		match := pattern.FindStringSubmatch(html)
		if match == nil {
			continue
		}
		target := absolute(website, strings.TrimSpace(match[1]))
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
			if e != nil || config.Width < minLogoPixels || config.Height < minLogoPixels {
				continue
			}
			// A brand mark is roughly square or a wordmark; anything much wider is a
			// marketing banner, and a banner in a store card is not the shop's sign.
			if ratio := float64(config.Width) / float64(config.Height); ratio > maxLogoRatio || ratio < 1/maxLogoRatio {
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
