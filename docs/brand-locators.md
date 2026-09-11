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
| Özdilek Home | 155 | rendered as markup, no coordinates published |
| IKEA | 10 | rendered as markup, points in data attributes |
| Chakra | 84 | rendered as markup, point on its own map button |
| Emsan | 18 | Karaca's group, Karaca's platform, same mapping |
| Çilek | 227 | two POSTs: provinces, then each province's shops |
| Vivense | 101 | rendered as markup, point per shop |
| Merinos | ~1,600 | rendered as markup, point on each map button |
| Weltew | 136 | rendered as markup, comma decimals |
| Pierre Cardin Yatak | 88 | rendered as markup, province and district on the card |
| Alfemo | 81 | whole list in the page, filtered client-side |
| Paşabahçe | 49 | same platform as Madame Coco |
| Korkmaz | 40 | rendered as markup, no coordinates |
| Dinarsu | — | WordPress admin-ajax, per city and per shop type |

**Look for the shared one first.** Three mappings covered seven brands: Bellona, İstikbal
and Mondi are all Erciyes Holding; Doğtaş and Kelebek share a dealer panel; Linens and Taç
share a commerce platform. Whoever maps the next one should check the page's own scripts for
a third-party host before assuming the chain runs its own endpoint.

## Not mapped, and why

**Nothing at the usual addresses.** The locator exists but is rendered by the page's own
scripts, or sits at a path the probe does not guess. These need a person to watch the
network panel once.

**What is left, and why.**

- **Koçtaş** — behind Akamai bot protection with a three-step form (province, town,
  district) and no endpoint that answers for a whole province.
- **Zara Home** — answers 403 to our bot for every page, and serves no robots.txt at all.
  Every other chain here serves us willingly. Reading it through a browser would mean
  presenting ourselves as a different client, and the data could not then be refreshed by
  the import that keeps every other brand current: it would be one stale island nobody
  would notice going wrong. Left out deliberately.
- **Nurus** — contract furniture rather than home and living; out of scope.
- **İpek Halı · Neva Home · Padişah Halı** — their sites time out or do not resolve. Nobody
  has found where these chains publish now.
- **Boyner Ev** — removed from the registry deliberately; it is a department inside a
  department store, not a network.
- **Deco Home** — `decohome.com.tr` does not resolve.
