
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
