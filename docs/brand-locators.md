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
| Bellona | ~515 | `brandapi.erciyes.com` — one request per province |
| İstikbal | ~700 | same API, `Brand=İSTİKBAL` |
| Mondi | ~250 | same API, `Brand=MONDİ` |
| Doğtaş | 252 | `services.dmgpanel.com/ajax/new-shops?mark=dogtas` |
| Kelebek Mobilya | 236 | same panel, `mark=kelebek` |
| Linens | 85 | `/api/inventory/inventoryGetStoreFilter` |
| Taç | 364 | same platform as Linens |
| Karaca Home | 171 | rendered into the page, one JSON object per shop |
| Tepe Home | 16 | rendered into the page as preloaded state |
| Yataş Bedding | 42 | SAP Commerce OCC, filtered by the chain's own code |
| Enza Home | 60 | same list as Yataş Bedding, other code |

**Look for the shared one first.** Three mappings covered seven brands: Bellona, İstikbal
and Mondi are all Erciyes Holding; Doğtaş and Kelebek share a dealer panel; Linens and Taç
share a commerce platform. Whoever maps the next one should check the page's own scripts for
a third-party host before assuming the chain runs its own endpoint.

## Not mapped, and why

**Nothing at the usual addresses.** The locator exists but is rendered by the page's own
scripts, or sits at a path the probe does not guess. These need a person to watch the
network panel once.

Çilek · Koçtaş · IKEA · Özdilek Home · Boyner Ev (its locator is a
single-page application; every path answers with the same 527 KB shell) · Chakra ·
Paşabahçe · Enza Home · Alfemo · Nurus · Merinos · Dinarsu · Emsan · Korkmaz

**Published inside the page, not behind an endpoint.** Karaca Home and Tepe Home render
their store lists into the HTML. Both are mapped now, through `extract` and `extract_after`
respectively; a chain that does this needs no new adapter, only the right marker.

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

**English Home's mark is inline SVG.** The collector looks for an image file; English Home
draws its wordmark directly in the page's markup, so the collector found only a photograph
of a phone and a pair of app-store badges and reported none. The mark is now in the web
application. A chain that does this is not rare, and the collector should learn to read an
inline `<svg>` out of the masthead.
