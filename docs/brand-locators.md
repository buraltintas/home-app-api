# Brand locators still to map

The registry (`internal/catalog/data/brands.yaml`) lists thirty-five chains. Three are
mapped and import cleanly. The rest are listed here with what `cmd/catalog -probe` actually
saw, so the remaining work is visible rather than living in somebody's head.

Mapping one is half an hour and no code: open the chain's store-locator page, watch which
endpoint its own scripts call, check `robots.txt` allows that path, and write the block.

```bash
go run ./cmd/catalog -probe -tier 1     # what is published at the usual addresses
go run ./cmd/catalog -source <slug>     # dry run once mapped
```

## Mapped

| Brand | Stores | Locator |
|---|---|---|
| English Home | 305 | `/api/Store/GetStoriesLite` — JSON, every field |
| Madame Coco | 673 | `/magazalar` — JSON, nested place object |
| Mudo Concept | 73 | `/stores` — same platform as Madame Coco |

## Not mapped, and why

**Nothing at the usual addresses.** The locator exists but is rendered by the page's own
scripts, or sits at a path the probe does not guess. These need a person to watch the
network panel once.

Bellona · İstikbal · Doğtaş · Kelebek Mobilya · Çilek · Yataş Bedding · Karaca Home ·
Koçtaş · IKEA · Tepe Home · Özdilek Home · Linens · Taç · Boyner Ev (its locator is a
single-page application; every path answers with the same 527 KB shell) · Chakra ·
Paşabahçe · Enza Home · Alfemo · Nurus · Merinos · Dinarsu · Emsan · Korkmaz

**The site refuses us.** A 403 on `robots.txt` itself, which this fetcher treats as "we do
not know what is allowed" and therefore does not proceed.

Vivense · Zara Home

**The address in the registry does not resolve.** Someone has to find the current one.

Deco Home (`decohome.com.tr`) · Mondi (`mondi.com.tr`) · Padişah Halı (`padisahhali.com`)

**Times out.** May be temporary; worth another run before treating it as blocked.

İpek Halı · Neva Home · Weltew

## Logos

Nineteen brands have a usable mark, collected by `cmd/brand-logos`. The others publish
none their own markup identifies, or are among the unreachable above.

**English Home has no mark on purpose.** Its first image whose path says "logo" is a
photograph of a phone and the next is a pair of app-store badges; a screenshot would be
worse than the initial letter its stores show now. Somebody can drop a real file into
`ui/public/brands/english-home.png` and rerun the collector to refresh the manifest.
