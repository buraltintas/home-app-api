# Turkish location dataset

`tr_locations.csv.gz` is every province, district and neighbourhood in Turkey with a
coordinate. It is generated, committed, embedded in the API binary, and loaded into the
`tr_locations` table by `cmd/seed-locations`. Nothing reads it at request time and nothing
is fetched from a provider.

## Regenerating

```bash
go run ./cmd/build-locations
go run ./cmd/seed-locations      # needs DATABASE_URL
```

Read the counts the build prints before committing the result. A sudden change in how many
rows are dropped means an upstream file changed shape, not that Turkey did.

## Where it comes from

| Source | Licence | What it supplies |
|---|---|---|
| [kratlg/turkey-geolocation-dataset](https://github.com/kratlg/turkey-geolocation-dataset) | MIT | every neighbourhood, with latitude and longitude |
| [turkiyeapi.dev](https://turkiyeapi.dev) | open API over public TÜİK/NVİ figures | province centroids, and the population of each province and district |

Neither is an official registry, and the coordinates are community-compiled, so the build
does not trust any single point:

- A **district's** position is the *median* of its neighbourhoods. One mistyped coordinate
  cannot move a median.
- A **neighbourhood** more than 80 km from its own district's median is dropped, not
  published. Turkey's largest district is about 120 km across, so this is generous for a
  real place and unmistakable for a broken row.
- Anything outside Turkey's bounding box is dropped before it is counted.

The last build dropped about 3% of the published rows on these rules and kept 81 provinces,
973 districts and 69,262 neighbourhoods.

## Names

Names are stored twice: `name` as a person reads it (`Kadıköy`), and `search_key` folded the
way `internal/textnorm` folds — lowercased, stripped of diacritics and punctuation
(`kadikoy`). Folding at write time is what lets an index answer `unca` for `Uncalı`.

Turkish casing is not Go's default casing. The registry publishes `BALIKESİR`, and
lowercasing its dotless I with the ordinary rules produces `Balikesir` — a different word.
The build uses `cases.Title(language.Turkish)`; if you touch that line, check Balıkesir,
Iğdır and Şırnak before committing.
