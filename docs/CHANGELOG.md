# Changelog — API

What has changed and why, newest first. Written for whoever picks this up next.

**No secrets here.** No keys, credentials, addresses or deployment values appear in this
file. Where a change was security-relevant it is described by its effect, never by
repeating the value involved.

---

## Renovation firms lose the categories they should never have had

- Eleven stores in the catalogue were service businesses classified as shops — a decoration
  search returned contractors, reported twice. `cmd/prune-services` removes their category
  links and nothing else: the store keeps its photo, rating and reviews, and simply stops
  claiming to sell what it does not sell. An unclassified store already sorts below
  classified ones, so it sinks rather than disappears. 1016 links became 995.
- The set is chosen by the classifier's own rule, not by a list anybody typed. That matters:
  a hand-written query found seventeen candidates, and the rule correctly keeps six of them
  — a VitrA dealer, a plumbing-supplies shop, a furniture shop that also fits what it sells.
- **The rule now matches stems, not whole words.** Turkish glues suffixes onto everything,
  so whole-word matching caught "Tadilat" and missed "Tadilatı" — leaving a renovation firm
  in the decoration category after the rule written to remove it had already shipped. Adding
  the inflections to a list would have been the same mistake as listing shop names: there is
  always one more nobody thought of.


## The brand list is gone

- Sixteen chain names were hard-coded so that Koçtaş and Madame Coco would classify. That is
  a patch wearing a rule's clothes: nobody can keep a list of Turkey's shops current, and
  the ones nobody thought to add stay broken silently.
- Measured before removing rather than assumed. With the provider's types stored, the list
  changed nothing for six of eight chains tested and added one extra category for two
  (Paşabahçe gained `tableware`, Cotton Box `home_textile`). That precision is not worth a
  rule that does not scale and has already misfired — "Taç" is a textile chain and also a
  language school, a clinic, a nursery and a housing development.
- `AGENTS.md` now states the principle: a rule naming a shop is not a rule. Prefer the
  provider's data because it is complete, then words the whole trade uses because every
  business of that kind uses them, then nothing. And when a report names one shop, fix the
  class — two renovation firms in a bug report turned out to be seventeen.


## A decorating firm is not a decoration shop

- Searching for decoration returned renovation contractors. Two causes, and the catalogue
  held seventeen of them. A firm calls itself "tadilat" and puts "dekorasyon" in the name
  too, so the name classifier read it as a decoration store — and Google types one of them
  as a `home_improvement_store`, so the provider agreed.
- What a business calls itself now beats the provider on this one point. A name carrying
  *tadilat*, *mimarlık*, *müteahhit*, *taahhüt*, *restorasyon* or *hizmetleri* yields no
  categories and is refused at import. This is the distinction the product rests on:
  somewhere you go to buy a thing, not somebody you hire.
- Deliberately narrow, because the rule takes real shops with it otherwise. *İnşaat* is
  left out — construction companies run showrooms, and "VitrA - Artema - Güvercinler
  İnşaat" sells bathroom fittings over a counter. So are *montaj* and *tesisat*: a shop
  selling the parts usually fits them too, and the shop is the part we want. A test pins
  both halves.
- Existing stores keep their categories. Seventeen are wrong and the fix does not reach
  them; stripping categories from live stores is a separate decision, and it is not mine.


## Settling classification and ordering

- **The remaining uncategorised stores were classified from Google's types**, fetched once
  for the fifty that had none stored and cached so a dry run and the apply shared one round
  of calls. 53 without a category became 40. The types are now kept for all of them, so
  this never needs asking again.
- The fetched types are **merged** into the external-source record, not written over it.
  That record also holds the rating, the photo reference and its required credits, and
  replacing it would have thrown all of that away silently.
- **Businesses Google is explicit about being something else are refused at import.** The
  catalogue had collected a bakery, a language school, two beauty salons, a ventilation
  contractor and the state opera's warehouse, because anything a search turned up was kept
  — and every one of them was being offered to Google for indexing as a home store. Only
  unambiguous types are refused, and silence keeps a place: most shops carry nothing but
  `store`, so demanding proof of belonging would empty the catalogue. Existing stores are
  left alone.
- `docs/architecture.md` now states the ordering rules and the classification rules in full.
  Both had been rediscovered from the code one report at a time, which is how the same
  question comes back.


## Classify from what Google says, not from what we guessed

- **The provider's types are now kept.** A store's categories are worked out once, at
  import, from its Google types and its name — and nothing kept those types, so when the
  classifier later learned to read something new there was no way to apply it to the stores
  already here without asking Google about all of them again. They are stored on the
  external-source record from now on, which makes reclassification a local operation.
- `primaryType` is requested and placed at the front of the list. Google returns a dozen
  types for a shop and most are noise — `store`, `establishment`, `point_of_interest` —
  while `primaryType` is its single best answer for what the place is.
- Thirteen more Google types are mapped. Koçtaş arrived with no category at all because it
  is a `building_materials_store` and nothing said what that meant.
- `cmd/reclassify` reads stored types first and falls back to the name. This is what makes
  the classification generic: types exist for every store in the country and describe the
  business, while a name only helps when somebody happened to put the product in it.
- **Ordering: distance now orders both halves of a name-led search.** Only the matches were
  sorted by distance, so among the other results a farther store could still lead a nearer
  one. Reported from the live site; there was no reason for it beyond an oversight.


## Backfilling categories for stores that had none

- `cmd/reclassify` assigns categories to stores that have none, and only to those. It reads
  that set once, writes nothing but `INSERT ... ON CONFLICT DO NOTHING` into
  `store_category_links`, never touches `stores`, and prints what it would do unless given
  `-apply`. The worst case of a bug is therefore a category that should not be there —
  visible, and removed by deleting one row.
- Run against production: 89 uncategorised stores became 53, adding 37 category rows.
  Verified before and after — no orphaned or duplicated links, no other table touched.
- **A bug worth recording, because it looked like the safe choice.** The insert was guarded
  with `WHERE NOT EXISTS (SELECT 1 FROM store_category_links WHERE store_id = $1)` — per
  store rather than per row. After the first category landed the store had a link, so every
  further category for the same store was silently dropped. One store lost its second
  category. The guard is now per row, which is what `ON CONFLICT` was already doing anyway.
- The `cam` rule was dropped before the run. It caught a mirror shop and a window-glazing
  company equally, and the rule this classifier is written to is that a wrong category is
  worse than none: it answers searches the store has no business answering.


## Ranking bounds and a wider classifier

- **A community review used to lead the searcher's whole city.** In Antalya that meant a
  shop ten kilometres away with three reviews beat a shop five hundred metres away with
  none — reported from the live site, and the ranking really did work that way. Reviews
  still lead, because they are the point of the product, but now only against stores a
  person would genuinely weigh against each other. The product owner set that at one
  kilometre. Two existing tests encoded the old rule and were rewritten rather than
  deleted, since the behaviour they described was deliberate until it was changed.
- **An unclassified store now goes last in a name search.** Searching for İşbir also finds
  a bakery called Isbirli, and a store we could not classify at all is the one most likely
  to be something else entirely. It sinks rather than disappearing: better a wrong shop at
  the bottom than a right one hidden because we failed to label it.
- The classifier also reads well-known chain names and several Turkish trade words that
  name a business plainly — *mefruşat*, *uyku merkezi*, *yapı market*, *hırdavat*, *cam*.
  The brand list is deliberately short: a name earns a place only when nothing else in the
  country shares the word. "Taç" is a home textile chain and a language school in
  Döşemealtı; "Karaca" is a homeware brand and a surname. A wrong category is worse than
  none, because it answers searches it has no business answering.


## Store classification and named-store ordering

- **Nearly a fifth of imported stores carried no category at all** — 89 of 496. The list
  included English Home, Nilda Home, Evim Home Goods and Deco Home Ev Aksesuar: shops whose
  entire business is in their name. A store with no category cannot be found by anything
  that filters on one, and shows a blank where its categories belong. The classifier now
  also reads the generic words that name a home store without naming a product ("home",
  "züccaciye", "ev aksesuar"), matched as **whole words** rather than substrings — which the
  product terms do not need to be, because "halı" inside a longer word is still about
  carpets while "home" inside "Homeros" and "ev" inside "Evren" are about nothing. A test
  pins both halves: the real shop names that were being missed, and the near-misses that
  must stay uncategorised.
- Existing uncategorised stores are unaffected until they are reclassified; the change only
  applies as stores are imported or refreshed.
- **A named store search ignored distance.** Typing a chain's name returned its branches in
  an order that meant nothing: every branch carries the same name and much the same score,
  and the distance penalty is capped for name matches precisely so a named store is not
  buried. Branches now run nearest first. Stores that are not the one named still appear —
  the nearest bed shop is worth knowing about when the branch you asked for is shut — but
  never above the ones that are.


## Admin surface, paid placement, contributor levels

- **`/v1/admin/*`** exposes the operator surface. Most of it is wiring rather than new SQL:
  `internal/reporting` already computed sixteen read metrics and none of them had a route.
  Raw-data reads that reporting does not cover live in a new `internal/admin` package, kept
  apart from the services that serve visitors so it stays obvious which queries need an
  administrator.
- Authorisation reuses the ordinary email sign-in rather than inventing a second credential
  to protect. `RequireAdmin` compares the signed-in address against an allowlist supplied by
  `ADMIN_EMAILS`. An empty allowlist closes the surface rather than opening it, and a caller
  who is not on the list is answered as if the route did not exist, because a 403 confirms
  that it does.
- **The allowlist is never hardcoded.** A privileged address committed to a public
  repository is a mistake this project has already had to undo once.
- Every privileged change writes an `admin_actions` row in the same transaction as the
  change, so the record cannot disagree with what happened.
- Account deletion runs through the existing user service rather than a second
  implementation, so administrator and self-service deletion cannot drift apart and the
  published account-deletion page keeps describing both accurately. Suspension revokes live
  sessions, or it would not take effect until a token happened to expire.
- **Paid placement** (`stores.is_premium`) leads results in the searcher's own city and no
  further. The flag reaches the client so the result can be labelled.
- **Contributor levels** — five tiers at 1/5/15/40/100 reviews, derived from the review
  count rather than stored, so deleting a review moves somebody back down instead of leaving
  a badge they no longer earned. Posts carry their author's level.

## Search

- **Ordering is tiered, not one weighted score.** A single score let a five-star store
  169 km away outrank one 14 km down the road, because distance cost only `distance/10000`
  while ratings were worth far more. Distance now bands the list: 500 m granularity below
  25 km, so the order reads nearest-first inside a city, with relevance deciding among
  stores that are genuinely equally reachable.
- A store already in the catalogue scored 80 when it had no community reviews, while the
  identical result seen only through Google scored past 100 — knowing a place pushed it
  down. Mapped stores now keep their Google standing until the community gives them a
  better one.
- Google is queried with `locationBias`, which it treats as a hint, so a search for bed
  linen in Antalya returned shops in Denizli and İstanbul. Results beyond 50 km are dropped
  while nearer ones remain, and kept only when the nearby area genuinely has nothing.
- **Store-name rescue.** Typing part of a store's name used to be answered with "request not
  understood" while the store sat in our own catalogue. The query is now matched against
  store and brand names — deliberately not city or district, since a bare city name would
  otherwise return every store in it. Three readings are tried strongest-first: all words,
  then consecutive phrases longest-first, then any word. Name-led searches rank by match
  quality rather than distance.
- Slug generation dropped every non-ASCII letter, so Turkish store names became unreadable
  in their own URLs. Letters are now folded to their Latin base. Slugs written before this
  still resolve.
- `ai_unavailable_or_invalid` covered three different incidents at once. They are now
  reported separately as `ai_unauthorized`, `ai_timeout`, `ai_invalid_response` and
  `ai_unavailable`, so one request is enough to tell a missing key from a slow provider
  from a model answering off-schema.

## Catalogue and email

- `GET /v1/stores/index` enumerates published stores for sitemap generation; search is
  query-driven and can never answer "every store you have".
- `GET /v1/stores/{id}` also accepts a slug, so shared links carry the store's name.
- A welcome email is sent to newly created accounts in their own language. It is enqueued
  inside the transaction that creates the account, so a rolled-back signup cannot leave mail
  behind, and it is keyed by user id so the serializable retry cannot send it twice.
  Reactivated accounts do not receive it: they are returning, not new.

## Documentation

- The README described Resend as the production email adapter. Production delivers through
  Gmail Workspace; Resend exists and is selectable but is not what runs, and saying
  otherwise sends anyone debugging delivery to the wrong provider.
- The README also published the store-review sign-in credentials in full. They were removed;
  because they remain in git history and this repository is public, **the code still has to
  be rotated in the deployment.**

---

## Known follow-ups

- `cmd/privacy-maintenance` implements every retention period the legal pages state, but it
  is a one-shot binary rather than a scheduler. If it is not scheduled in production, none
  of those stated periods is true.
- Premium has no expiry: nothing switches it off by itself. The audit log records when it
  was turned on, which at least makes the question answerable.
