## Independent shops from OpenStreetMap (2026-09)

The catalogue is chains, because chains publish lists. The shops that are nobody's branch do
not, so they come from the public map instead. Three things a lawyer should see:

- **The licence is ODbL, not public domain.** OpenStreetMap data may be used commercially,
  but two obligations travel with it: attribution ("© OpenStreetMap contributors", with the
  licence named and linked, wherever the data is shown) and share-alike on a *derived
  database*. The second is the one that needs a view. Our catalogue mixes OSM rows with rows
  compiled from chains' own lists and with our own reviews; whether that makes the whole thing
  a derived database, or whether the OSM-sourced rows are a separable collective work, decides
  what — if anything — we would have to publish. Every row that came from the map is tagged
  `source_kind='osm'` and carries its map object id, so the OSM-derived subset can be
  identified exactly, whichever answer we get.

- **Attribution is shown on every store that came from the map.** The store page credits
  "© OpenStreetMap contributors" and links the licence, beside the address the data
  describes, so the credit travels with the row rather than sitting on one distant page.
  Rows are driven by `store_external_sources.provider='osm'`, so a row that is later
  re-sourced from a chain stops claiming a credit it no longer needs.

- **These rows are not verified and are not presented as verified.** Nobody published them
  about themselves; a mapper wrote them down. They carry no `data_verified_at`, search ranks
  them behind rows a chain confirmed, and the store page says a location is approximate when
  it is. A shop that objects to being listed is a case we should have an answer for before
  this is public, in the same way a chain would be.

---


## Removing Google Places (2026-09)

Three things changed that a lawyer should see, all of them in the direction of fewer
obligations rather than more:

- **A foreign recipient was removed.** Search queries, coordinates and search radius are no
  longer sent to Google Places. The privacy policy's recipients table and the KVKK transfer
  table both lost that row. What remains of that kind is the search text sent to OpenAI to
  work out intent -- coordinates are not sent there -- and Google sign-in, which is
  unchanged.
- **Store data now has a different origin, stated in the documents.** Names, addresses and
  coordinates are compiled from the chains' own published store lists rather than bought
  from a provider. `docs/brand-locators.md` records which chains and where from; the fetcher
  obeys each site's robots.txt and identifies itself. Two questions worth a view: whether
  reading a published store locator raises any issue under the site's own terms, and
  whether a chain's logo, shown to identify that chain in a directory, is nominative use.
- **Location verification is no longer required to write a review.** It remains as a
  "verified visit" badge and a ranking signal. Anything published that said only verified
  visitors may review has been changed; if a marketing page still says it, it is now false.
