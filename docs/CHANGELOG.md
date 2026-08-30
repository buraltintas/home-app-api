# Changelog — API

What has changed and why, newest first. Written for whoever picks this up next.

**No secrets here.** No keys, credentials, addresses or deployment values appear in this
file. Where a change was security-relevant it is described by its effect, never by
repeating the value involved.

---

## A delicatessen and a lokanta were still filed under tableware

- Two shops reported from the live site, both reading "Sofra" on the sign. The classifier
  was reading the provider's trade before the sign, which was the earlier fix -- but one of
  them is typed `liquor_store`, which was not in the list of trades that are plainly not
  ours, and the other carries no telling type at all ("manufacturer", "establishment"), so
  the sign was everything.
- Food and drink retail is now named in that list: an off-licence, a delicatessen, a
  butcher, a greengrocer. The provider says what they are.
- And where the sign is all there is, the food trade's own vocabulary gets to veto. A word
  can name two trades -- "sofra" is what a dinner service is called and what half the
  country's eating houses are called -- and a word that names two cannot decide alone.
  "Bizim Sofra Günlük Ev Yemekleri Kahvaltı" says what it is twice; only one reading was
  being heard.
- Matched from the start of a word, allowing what Turkish adds to the end of one. A plain
  substring match threw "Tekeli Kilim", a carpet shop, out of the catalogue; a whole-word
  match missed "Lokantası". Oven and hob are deliberately absent from the vocabulary,
  because both are kitchen appliances.
- Two stores in the catalogue were reclassified by the existing prune command, which reads
  the provider types already stored. No provider call.

---

## Searching a chain listed its branches in other provinces

- Reported: an "İşbir" search from Antalya returned İşbir branches in provinces the
  searcher had searched from on other days. Each of those had been learned from those
  searches, and the name matched, so distance stopped applying at all.
- Dropping the radius for a name-led search was deliberate -- a store somebody names is
  worth finding wherever it is -- but too broad. If the name they typed exists inside the
  50 km horizon, that is the answer, and a branch four provinces away is not part of it.
  Only when nothing nearby carries the name is the nearest one anywhere worth showing,
  which is the case the loosened rule was written for.

---

## A search for "yastık" answered with a shop 44 km away, in second place

- Reported from the live site: a search from Antalya put a store in Serik, 44 km out,
  above a dozen at eight kilometres.
- Two faults, one on top of the other. A pillow is bedding and the word appeared in none
  of our vocabulary, so "yastık" classified as nothing: no category, no measured relevance.
  The intent parser, given a word we could not explain, read it as the name of a shop --
  and a name-led search deliberately drops the radius filter and lifts every store whose
  sign carries the word above every store that is nearer. Working exactly as designed, on
  a premise that was wrong.
- The vocabulary now names pillows, mattresses and bed bases in four languages, beside the
  bed it already knew.
- And a parsed store name is now checked against the trade's own vocabulary before it is
  believed. Strip the words that name a product, a category or a shop, and if nothing is
  left then nothing was named -- so "Yastık" is a product while "Bambi Yatak" is a shop.
  The test is the same table used to classify every store in the country, not a list of
  words anybody noticed.

---

## Every store's city carried a postcode, and no store had a district

- A Turkish address ends "... 45003 Yunusemre/Manisa, Türkiye" and the whole of that
  component was stored as the city. Store pages were titled with a postcode nobody asked
  for, and the district column was empty for the entire catalogue.
- New imports have been correct since the parser landed; the 837 stores that arrived
  before it were not. `repair-store-locations` re-reads the addresses we already hold and
  separates them. No provider call: the information was always in the row, it was simply
  never split.
- The parser also mishandled an address with three levels. "Bahtılı Köyü/Kepez/Antalya"
  stored "Bahtılı Köyü/Kepez" as the district, which is not a district and groups with
  nothing. Only the level next to the province is the district.

---

## The provider is now asked only when we cannot answer ourselves

- Every search asked Google in parallel with our own catalogue, whether or not the
  catalogue could already answer it. That is a bill that grows with traffic rather than
  with the product. On the searches on record, three quarters of them ended with Google
  adding no store we did not already hold -- each of those was paid for and changed
  nothing a person saw.
- A sufficiency gate now runs between the local search and the provider call. It asks four
  things, and all four have to hold before the call is skipped: enough results, results
  that are actually about what was asked, enough of the surrounding catalogue held for
  their absence to mean anything, and no store named outright. Somebody who types "Yatas
  Atasehir" wants that store, and a wall of similar shops is not an answer to a name.
- Counting results alone would have been the wrong rule. Measured on our own ranking, the
  stores only Google knows about score a median 114 against 70 for the ones we already
  hold: when the provider adds something, it tends to add something good. So relevance is
  a condition in its own right, not a tie-breaker.
- Nothing after the provider call changed. The home-and-living filter, place_id
  deduplication, the catalogue import, persistence and ranking all run exactly as they
  did. Only when the call starts is different.
- Behind `SEARCH_LOCAL_FIRST_ENABLED`, off by default. With the flag off the search
  behaves exactly as it does today, including asking the provider in parallel, and the
  recorded reason says `gate_disabled`. One configuration change reverts the behaviour.
- Every threshold lives in one policy struct fed from configuration, because the right
  values are not knowable in advance. They start deliberately conservative: eight results,
  0.6 relevance, forty stores known within fifteen kilometres. The first version is not
  trying to reach the ceiling the historical data suggests -- 55-65% local-only on real
  traffic would be a normal and good start.
- A 5% sample of local-only searches asks the provider anyway, after the answer has gone
  out, purely to find out what the decision cost. It is read, never imported: the
  catalogue does not learn from a measurement. The number it produces is the High-Relevance
  Miss Rate -- how often staying local cost somebody a store that would have outranked
  everything they were shown.
- Recorded per search: the decision, every failing condition, the measured relevance, how
  much catalogue was held nearby, and the local, provider and total latencies, with the
  city and district so a national rate cannot hide the towns where the catalogue is thin.

**Known consequence, not yet addressed.** A store's Google photo is only shown while its
stored provider record is less than thirty days old, and today every search refreshes the
records it touches. Asking the provider less often means refreshing less often, so a store
that stops appearing in fallback answers will quietly lose its photo in search results
after a month. The fix is a background refresh keyed on `refreshed_at`, which costs one
call per store per month instead of one per search -- deliberately out of this change, and
worth doing before the flag is turned up.

---

## A place import wrote a district column that had no value bound to it

- The insert named ten parameters and passed nine. Every store imported from the provider
  would have failed on the missing bind. Caught before it shipped; the district parsed out
  of the address is now actually passed.
- A Turkish address ends "... 07070 Konyaalti/Antalya, Turkiye", and the whole of that
  last component was being stored as the city. That is why a store page was titled with a
  postcode nobody asked for and why the district column was empty for the whole catalogue.
  City and district are now separated, and the postcode dropped.

---

## The integration suite did not compile

- `slices` was used and never imported, so `make integration-test` failed before running a
  single test. Nothing was wrong with the tests themselves.

---

## The list and the store's own page named its categories differently

- Reported: the results list said "Nevresim takimi" and the store's own page said "Yatak",
  for the same store and the same category.
- Two sources of truth. Search results carried category slugs and the client translated
  them from a list of its own; the store's page carried names translated in the database.
  The two had drifted, and nothing would ever have brought them back into line.
- Search results now carry the names as well, read from the same translations the store's
  page reads. The client's own list survives only as a fallback for a result that is not
  in the catalogue at all -- one that has no page to disagree with.

---

## Every restaurant called "... Sofrasi" was a tableware shop

- Reported: a Turkish restaurant was filed under Sofra Takimi, and searching "sofra"
  returned restaurants. The report asked whether the category should be dropped. It should
  not -- tableware is a real thing people shop for. What was wrong is who got the last word.
- The classifier read the shop's own sign before the provider's verdict. That ordering was
  written for a good reason: Google types a curtain shop that also hangs curtains as a
  general contractor, and the sign is the better evidence there. But it also meant no type
  could ever overrule a product word, and "sofra" is both a real word for tableware and
  what half the kebab houses in the country are called.
- The disqualifying types were doing two different jobs under one name. Some name a
  business that sells labour -- contractor, service -- and Google gives those to shops too,
  so they are still judged after the sign. Others name a different business altogether --
  restaurant, pharmacy, gym, hotel -- and no word on a sign makes a kebab house a shop.
  Those are now judged first.
- 48 stores in the catalogue were wrong in exactly this way, and the shapes go well beyond
  the one reported: hairdressers called "Ayna" under Decoration, six football pitches
  called "Hali Saha" under Carpets, a fruit and vegetable market hall likewise, an English
  school called "English at home" under Home Accessories, cooking workshops under
  Kitchenware. Their category links are gone; the stores keep their photographs, ratings
  and reviews and simply stop claiming to sell things they do not sell.
- The correction was worked out from the provider types already stored against each store.
  No request to the provider was made, for any of it.
- The two functions that decide this can no longer disagree: the one that works out what a
  store sells now asks the one that decides whether it belongs here at all.

---

## Opening hours, for nothing

- Asked for: show a store's working days and hours in the result list.
- The provider bills a request at the tier of its most expensive field, and the field mask
  already asks for the rating, the website and the phone number -- all of which sit in the
  same tier as opening hours. So the hours arrive on the request we were already paying
  for: no second lookup, no per-store charge, no change to the bill at all.
- Stored with the rest of what the provider says about a store. Both what it publishes for
  a reader -- already phrased, already in the right language -- and the raw periods, which
  are what can answer "is it open right now" across four languages when the sentences
  cannot be parsed back.
- "Open now" is worked out when the hours are served and never stored, because a stored
  answer to that is wrong within the hour. It is worked out in the store's own time, not
  the reader's: whether a shop in Antalya is open does not depend on where the person
  asking is standing. A shift that ends after midnight belongs to the day it started.
- A store that publishes no hours gets no answer, rather than being shown as closed.

---

## The same search, paid for twice

- Every search called the provider, including the ones asking a question already asked. Of
  the searches on record, 67% repeat a query already made from the same area -- "yatak" 92
  times, "salon icin buyuk bir ayna" 56 -- and each repeat was a separate charge for an
  answer that had not changed.
- Provider answers are now kept for six hours, keyed by the question: the query folded the
  way the classifier folds it, the position rounded to about a kilometre, the radius and
  the language. Two people a street apart are asking the same thing and it is bought once.
- The obvious alternative was measured first and rejected. Skipping the provider when the
  catalogue already returned ten or more results would have covered 48% of searches -- but
  in 24% of those the provider was bringing back a store we did not hold, and raising the
  threshold to twenty barely moved it (21%). That saving comes out of what a person can
  find. Caching the question costs nothing anybody would notice; declining to ask it costs
  exactly the thing the product is for.
- Writing the key surfaced a Turkish bug worth keeping: Go's ToLower turns "HALI" into
  "hali" rather than "halı", so the same query typed in capitals would have missed its own
  cached answer. The key folds the way the rest of the search folds.
- A cache write can never fail a search. The worst case of losing one is paying for the
  same question twice, which is what happened before it existed.

---

## A search for a bed brand returned a bakery

- Reported: searching "İşbir" returned "Isbirli Ekmek Taş Firin".
- Our own catalogue search is not what did this. Asked for "İşbir" it returns the İşbir
  Yatak branches and nothing else -- checked directly against the index. The bakery came
  from the provider, which matches names loosely by design, and "Isbirli" resembles
  "İşbir" enough for it.
- What put it in front of a reader, and then into the catalogue for good, was ours. The
  classifier had already declined it: it returns no categories for a service, a workshop,
  a bakery. The import ignored that verdict and wrote the row anyway, with no categories
  at all. 59 of 838 stores had arrived that way -- technical services, a sports complex, a
  motorbike rental.
- A place that classifies into none of our categories is not a store for this product. It
  is no longer imported, and no result without a category is shown, wherever it came from.
  A store already in the catalogue keeps its own categories, which may have been set by
  hand, rather than having them re-derived.
- Deciding what belongs here was always ours to do. Loose matching is the provider working
  as intended; the filter is the part we had not written.

---

## A chain lost its branches before anything could rank them

- Reported: searching a chain's name returns fewer branches than the chain has, and a
  second search for the same word returns more than the first.
- The name search was capped at twenty matches while a search is allowed to return thirty,
  and the caller had already been changed to ask for thirty. The cap sat below both, and it
  cut before ranking: a chain with more branches than the cap lost the far ones outright,
  so distance never got to decide which branch a person was shown. Madame Coco has twenty
  branches in one city alone; English Home has fifteen.
- The cap is now the same number a search is allowed to return, and it is a named constant
  next to that number rather than a figure repeated in two places that drifted apart.
- The second half of the report is not a defect and is worth writing down so it is not
  chased again: the first search imports the branches it finds from the provider, so a
  later search finds them in our own catalogue as well. More results the second time is the
  catalogue filling in, not the search behaving differently.

## The closure warning had nothing to warn with

- Reading the status was one half of it. The value is written when a store is imported or
  when a search turns it up again, so on the day the warning shipped not one store in the
  catalogue held it: the feature was complete and entirely silent, and would have filled in
  slowly, store by store, as people happened to search.
- `cmd/backfill-status` asks once for the stores already here, in the same shape as the
  other maintenance commands: only a record holding no status is read, the write merges
  into the existing attribution and is guarded again in the statement, nothing else in that
  record is touched.
- Applied to all 583 active stores. Google calls 581 of them open and two permanently
  closed, and those two now carry the warning wherever they appear.
- Worth recording for whoever picks up the report that prompted this: the store it named is
  one Google still calls open, so this warning will never fire for it. Google's own "is this
  place closed?" poll is not published through the API, and no amount of reading the
  provider harder will surface it.

## A closed-store signal was fetched by nobody

- Google Places publishes an explicit business status, but neither text search nor place
  details requested it. A closed branch therefore looked identical to an operational one
  even when Google had already made the distinction.
- Search and detail now request, persist and return that provider status. Clients can warn
  for temporary and permanent closure consistently on result and detail surfaces. Review
  age and consumer polls are deliberately not treated as closure evidence: the Places API
  does not expose the poll, and an old review says nothing reliable about whether a quiet
  but legitimate shop still trades.

## Store-name searches no longer depend on which search happened first

- A finite list of familiar brands made a full store name call Google while an unfamiliar
  or partial name searched only the local catalogue. The first partial search therefore
  returned whatever had previously been imported; searching the full name warmed the
  catalogue; repeating the partial search suddenly returned more branches. Brand names
  are no longer classification rules.
- Unclear text now checks both catalogue names and provider results, then applies the same
  provider-type and trade-word classifier used for every business in Turkey. Definite
  out-of-scope requests still do not call the store provider. Name-led searches use the
  documented local horizon and can return up to the API's thirty-result cap.
- Tire and auto-parts provider types are explicitly outside home and living, closing the
  generic false-positive path opened by provider lookup for an unfamiliar name.

## Stored Google identity is useful even without a rating

- Internal search results attached their stored Google source only when that source also
  had a rating count. A legitimate store with a Google place id, phone or photograph but
  no reviews therefore showed those facts in search and lost them on other surfaces; in
  particular the Google Maps action disappeared. The source is now attached whenever it
  exists, while missing rating fields continue to decode as zero.

## Twelve stores had a photograph we never went back for

- Reported: one store shows no photograph. Google has none for it either -- nothing to
  fetch, and the empty state is the honest answer. Looking at the class it belongs to found
  the real defect: 34 stores were showing nothing, and Google has photographs for 12 of
  them today.
- A store's photo reference is captured at import and refreshed whenever a search turns the
  store up again. Google's photographs arrive over time, somebody visits and uploads one,
  so a store that had none on the day it was imported may well have ten now -- and nothing
  ever went back to look.
- `cmd/backfill-photos` closes that once, built like the other maintenance commands: only a
  record holding no photo reference is read, the write merges into the existing attribution
  and is guarded a second time in the statement, nothing else in that record is touched,
  and applying takes `-apply` and one transaction. The credits Google requires to be shown
  with a photograph are stored with it and never separately.
- Applied: 12 stores gained their photograph, 437 to 449. The remaining 22 genuinely have
  none anywhere, and for those the invitation to contribute one is the right screen.

## Two thirds of the catalogue had been sorted blind

- Google's types were not kept when older stores were imported, so 343 of 517 stores were
  being classified on their name alone. They are the generic evidence -- they exist for
  every business in the country and say what the place is -- so they were fetched once and
  stored. Every store in the catalogue now has them.
- Three defects surfaced while aligning, each caught by a dry run before anything was
  written, and each fixed in the classifier rather than in the data:
  - **A trade could not outvote a sector label.** The first positive type won outright, and
    Google gives `home_improvement_store` and `home_goods_store` to painters, roofers and
    contractors alike. A renovation firm with `general_contractor` written in its own type
    list was walking into a catalogue of shops on the strength of the label beside it.
    `painter`, `roofing_contractor`, `electrician`, `plumber` and `interior_designer` are
    now read as what they are, and a type that names what is sold still wins outright, so a
    real showroom typed into the building trade is not thrown out with them.
  - **The shop's own sign was not evidence.** A curtain maker typed `general_contractor`
    and a carpet shop typed `clothing_store` were both about to be struck off. A name that
    plainly says "perde" or "halı" now outranks a type list already seen to be wrong.
    "Dekorasyon" and "depolama" are excluded from that rescue: the first is on the sign of
    every renovation firm in the country and the second is warehousing, not a wardrobe.
  - **Sector labels were handing out categories.** Every carpet and curtain shop was being
    added to Ev Aksesuarları as well as its own category, 280 rows of it. They are now a
    fallback, answering only for a shop nothing else describes.
- Nothing was removed. Removing whatever the classifier could not re-derive was tried and
  the dry run caught it taking `bedding` from "Yataş" and from a shop called "Nevresim
  Takımı", so the rule was buying three rows and risking real data.
- Applied: 50 businesses retired -- storage yards, renovation firms, movers, a hairdresser,
  a language school, a clinic, a bakery, an apartment complex -- and 87 categories added to
  84 stores. Active stores 517 to 467, category links 1007 to 1094, stores with no category
  51 to 16. Retirement is `deleted_at`, so all of it reverses with one statement.
- **No store carrying a review, a favourite or a verified visit is ever retired**, whatever
  the classifier says. Community work outranks a directory. None qualified this time; the
  guard is there for the next run.

## A website Google Maps showed and we did not

- Reported: a store's website is visible on Google Maps and missing here. The import reads
  both the website and the phone number, and a store that turns up in a search again has
  them filled in from the search response at no extra cost. That covers everything anyone
  has looked for since; it covers nothing at all for a store nobody has searched for since
  those fields started being read. 492 stores were sitting in that gap.
- `cmd/backfill-contact` closes it once. It is built like `cmd/reclassify` and for the same
  reason -- it runs against the production database, so only a store with an empty value is
  read at all, the write is guarded a second time in the statement itself, nothing is
  inserted or deleted, and applying takes `-apply` and one transaction. Fetched details are
  cached on disk so a dry run and the apply that follows it do not each cost a round of
  provider calls.
- Both fields are asked for in one request on purpose. They sit in the same billing tier,
  so asking together costs what asking for either alone would: 492 calls instead of 641.
- Applied: 264 websites and 102 numbers filled in. Stores with a website went 26 to 290,
  stores with a number 367 to 469, the catalogue unchanged at 517. The provider had nothing
  for the remaining 176.
- This should not become a scheduled job. A store people actually search for is refreshed
  by the search itself, for free; a recurring run would pay for that same data again.

## We were poisoning our own search query

- A search for "yatak" near Bostanlı returned twenty results and every one was a branch of
  the same chain. The provider was not the problem — asked the same word directly it returns
  six different shops in six.
- The problem was ours. The provider query was built from the person's words **plus our
  parsed intent**: product terms, semantic terms and category slugs, on the theory that more
  terms match better. Measured, the opposite:

  | sent to the provider | branches of one chain, out of six |
  |---|---|
  | `yatak` | 2 |
  | `yatak bed` | 2 |
  | `yatak bed bedding` | 4 |
  | `yatak bedding` | **6** |

  `bedding` is our internal key. It is also half the name of a chain with a branch on every
  corner, and a provider that matches text literally does what it is told.
- The provider now gets the person's words and their location. Nothing else. This is not
  about that chain: every category key we have is an ordinary English word that some Turkish
  business has put on its sign, so any of them could have done this. The categories still do
  their work where they are keys rather than search terms — filtering our own catalogue.


## Standout stores get a face

- The heading loses "bu ay": the window is a month and stays a month, and saying so in the
  title spends words on something nobody is deciding anything with.
- Each highlight now leads with the store's photograph, the same one and picked the same way
  as in the result list — a community picture ahead of the provider's. A recommendation made
  of a name and a number reads as a statistic; with a picture it reads as a place.
- Where no photograph exists the initial stands in, on the same footprint, so the row does
  not change shape between stores.


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
