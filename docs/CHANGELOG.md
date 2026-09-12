# Changelog — API

What has changed and why, newest first. Written for whoever picks this up next.

**No secrets here.** No keys, credentials, addresses or deployment values appear in this
file. Where a change was security-relevant it is described by its effect, never by
repeating the value involved.

## "Where can I buy a Yataş bed round here" now has an answer

One dealer holds two franchises and appears in both chains' published lists. The importer
already handles that without adding the shop twice: it records the second chain as a brand
the shop carries. Nothing read that record, so a search for a chain found only shops wearing
its sign -- and "Gülseven Mobilya" in Tekirdağ, which sells İstikbal, answered an İstikbal
search with nothing.

A search naming a chain now also finds the shops that carry it. Matched on the brand's own
folded name against the folded query, which is the same comparison the rest of this search
already makes, so it costs nothing when the query names no brand. Four Bellona dealers that
also sell İstikbal answer an İstikbal search today; the number grows on its own as more
cross-brand dealers are recognised.

---

## Two rows that are one shop can be made one shop

The matcher is deliberately cautious about joining rows on its own -- a wrong merge is
silent and permanent in a way a duplicate is not -- which means the ones it declines have to
be joinable by hand, and until now they were not.

`POST /v1/admin/stores/{id}/merge` moves everything the other row carried onto this one:
reviews, verified visits, search results and interactions outright; favourites, categories,
source identifiers, carried brands, attributes and translations where they do not collide
with one the survivor already has. The survivor keeps its own id, because that is what every
review, favourite and rating in the database already points at, and it takes the stronger of
the two provenances: a brand link, an address, a phone number or a verification date present
on only the row that went is not lost with it.

The other row is not deleted outright. It is soft-deleted with `merged_into` pointing at the
survivor, and a request for it -- by slug or by id -- is answered with the survivor, so a
link somebody shared before the merge still opens a shop. One hop only: a chain of merges is
something nobody should be able to build, and following one forever is how a loop becomes a
hung request.

Counts are recomputed from what is actually there afterwards rather than added up from the
two rows, which is the only version that stays right when a review moved and a favourite did
not.

**It shipped with a defect and the first exercise of it found one.** Source identifiers were
copied and then the originals deleted, like everything else -- but that table's uniqueness is
on the identifier alone and not on the shop holding it, so copying a row that already exists
conflicts with itself, does nothing, and the original goes with the shop. A dealer a chain
had listed twice lost the second of its two identifiers: the one piece of evidence that would
have let the next import recognise the row instead of proposing it all over again. Sources
are moved now, not copied. Measured on a real pair inside a transaction that was rolled back:
before the fix the survivor ended with one identifier, after it with both (2301 and 2302).

`cmd/merge-stores` runs the same service the panel's button does, and exists for the cases
the panel is awkward for -- a cleanup pass over rows found by a query, and exercising code
that deletes things somewhere a person can read the result rather than in production on the
first press. It records the merge against the administrator who ran it, by email, and refuses
if that is not somebody real: an unattributable entry in the audit log is worse than no tool,
because the next person reading it cannot ask anybody what they were looking at.

---

## A shop we placed ourselves says so

Five hundred shops in the catalogue stand where we put them, not where their chain says
they are: the chain publishes no usable coordinate, so the row is stood at the centre of the
smallest place its address names. The database has recorded that all along, in
`location_from`, and the product said nothing -- the map pin and the distance looked exactly
as certain as everybody else's.

The store DTO carries `location_approximate` now, the store page says it under the address,
and a distance to such a shop is written with a tilde and a line saying why. One more thing
follows from it: "close enough to review" is a claim about where the reader is standing, and
it cannot be made from a point we invented, so that badge no longer appears on an
approximate shop. The shop could be half a kilometre from where we put it.

---

## The catalogue re-reads itself now

Every shop in it came from a list a chain publishes and keeps changing: branches open, move
and close. Re-reading one meant a person running the command or pressing the button, so the
honest answer to "how fresh is this" was "as fresh as the last time somebody remembered".
Today's full sweep was started by hand.

The worker now re-reads one brand every three hours -- the one read longest ago. One at a
time, and the oldest first, for three reasons: it spreads the load over days instead of
hammering thirty sites in an hour; a failure costs that brand its turn rather than the whole
sweep; and it levels itself, because a brand registered today is the oldest thing in the
table and is read first, after which everything drifts into the same rotation with no
schedule written down anywhere. A registry of thirty comes round about every four days.

It is in the worker rather than the API because it is slow, it is nobody's request, and it
must not be doubled by the API running more than one instance. `CATALOG_REFRESH_HOURS=0`
turns it off, which is what a development machine wants: it should not fetch somebody's
website because a laptop was left running.

---

## Three provinces of Merinos dealers had been thrown away

A point outside Turkey's bounding box meant the row was not a Turkish shop and was dropped.
That is right for Weltew's Tbilisi branch and Madame Coco's Erbil ones. It is wrong for a
Konya shop a chain published at a coordinate in Iraq -- and Merinos does that to hundreds of
its dealers. Merinos had no shops at all in Bilecik, Yozgat or Ağrı: every dealer there
carried a junk coordinate, and the catalogue quietly refused the lot.

A row's own words come before its coordinate here as everywhere else. A branch name routinely
says where it is -- "Cihanbeyli Konya - Yeniceoba" -- so when nothing else names a place the
name is read the way an address is: a province must be named before a district inside it is
looked for, which is exactly what stops "Özbekistan - Taşkent" becoming Konya's Taşkent. A
point outside the box is still never used; what changes is that the row goes with it only
when the row names no Turkish place either.

Measured on Merinos: 407 rows skipped before, 48 after -- 353 real dealers recovered,
including the first ones this catalogue has ever had in three provinces. Foreign branches are
still refused: Georgia, Iraq, Libya, Uzbekistan and Tajikistan all still drop out by name.

**And a point we cannot place in Turkey at all now says so.** Turkey's bounding box takes in a
good part of four neighbours -- Batumi, Tbilisi, Erbil and Duhok all sit inside it -- and the
open sea, which is where a coordinate rounded to whole degrees lands. A row that resolves to
no Turkish town within fifteen kilometres and names no place of its own is not a Turkish
shop; it is refused, and an existing row carrying the same identifier is soft-deleted, so a
rule newer than the catalogue can clean up after itself instead of only refusing new arrivals.

---

## A hundred shops on one doorstep

Merinos gives 104 of its dealers a single point in Bursa and 45 more a single point in
İstanbul -- its own address, presumably, filled in wherever the dealer's was not known. The
catalogue believed every one of them. A hundred shops stood on one doorstep, all the same
distance from any visitor, and "the nearest one to me" answered with whichever of them the
sort happened to put first.

A coordinate a publisher has given to a great many of its shops at once is not any of their
addresses -- the same test the neighbourhood table already gets, where a centre shared
across districts stands for no district. Five shops of one chain on one point to the metre
is the line: two shops in a shopping centre can honestly share a point, five cannot. Such a
row is treated as publishing no coordinate at all and is placed from the rest of its
address: its neighbourhood, then its district, then its province, saying so in its
provenance.

The importer refuses these on the way in. `cmd/unstack-stores` applies the same rule to rows
already held, and fetches nothing -- the address was always in the row. 296 shops moved, 139
of them to the neighbourhood their own address names; 4 were left where they were because
their row does not say which province they are in, and inventing one is worse than a bad
point. Shops sharing a point with four or more others: 339 before, 170 after, and every one
of those remaining is a district centre we placed and marked as ours.

---

## The last brand that could not be imported, and why it could not

Vivense was refused by the twin guard on every run: seven of its hundred and one shops could
not find the rows they already had, and would have been added a second time. Two things were
in the way, both of them general.

**A chain that leaves the district field empty has often put the town in the branch name.**
Vivense publishes "Antalya Kepez Satış Noktası" with no district at all. The importer read
the district from the row's coordinate instead -- and Vivense's coordinates are wrong by
hundreds of kilometres, so the shop was filed in Muratpaşa and stopped recognising itself.
The row's own words now come before its coordinate. Four of the seven.

**A district narrows a search; it is not required to make one.** A row with neither a usable
coordinate nor a district -- Vivense's Erzurum, Karabük and Kastamonu shops name only their
province -- found nothing to compare itself with and was added again every time. The city
alone is enough to look in, because what decides a merge there is the name, and it has to
clear 0.75 similarity rather than the 0.55 that a merge backed by a coordinate needs. The
other three.

Vivense now imports: 101 fetched, 0 new, 101 updated. Every mapped brand in the registry
imports cleanly.

---

## A branch name says which town it is in, and that beats a coordinate we invented

Reading a district off a point we had placed ourselves left rows filed in the wrong town,
and once written, the wrong district is indistinguishable from a published one -- the rule
that stopped doing it could not undo what it had already done. The row's own name is the way
back: Vivense calls its shop "Antalya Kepez Satış Noktası" while the coordinate we invented
for it says Muratpaşa, because Muratpaşa is what lies nearest the middle of Antalya.

For a row standing on a coordinate we placed, a district named in its own name or address now
wins. Held inside the province the row is already known to be in, so a word that happens to
be a district somewhere else in Turkey cannot move a shop -- that unbounded version once
filed an İstanbul shop in Diyarbakır.

Sixteen rows moved, every one of them into the town it is actually in: Alfemo's Masko shops
from Fatih to Başakşehir, its Forum Bornova shop from Konak to Bornova, Ankara Siteler from
Çankaya to Altındağ, Boyner's Çiğli Kipa from Konak to Çiğli.

---

## A place must not be read off a point we placed ourselves

The rule that lets a shop's coordinate name its city and district was applied to rows whose
coordinate we had invented -- a shop with no published point is stood at the centre of its
town, and reading the town back off that point is a circle. It put Vivense's Kepez shop in
Muratpaşa, because Muratpaşa is what lies nearest the middle of Antalya, and the next import
then could not recognise its own row. The pass over existing rows now ignores a coordinate
whose provenance says we placed it.

**And an import that failed no longer reports as an idle one.** A run where every brand
attempted errored printed "nothing to do: no registered brand has a mapped store locator
yet", which is a registry waiting to be filled in, not a rolled-back import. It now says how
many were attempted and points at the errors.

---

## A shop with no coordinate stands in its own neighbourhood, not in the middle of town

A chain that publishes an address and no coordinate has still said, in the first phrase of
that address, where in the town its shop is: a Turkish address begins with its
neighbourhood. That was being thrown away -- the shop was stood at the centre of its
district, which in İstanbul can be ten kilometres from where it actually is, and the
distance shown to a visitor was wrong by that much.

The fallback is now the neighbourhood the address names, then the district, then the
province. Looked up only inside the district the row is already known to be in, so a name as
ordinary as "Cumhuriyet" or "Merkez" can only mean the one place it means there; a name that
is not unique even inside its own district is not guessed at. Twenty-two of Boyner's
fifty-nine shops moved from the middle of a district to the street they are on.

It is still a derived point and still says so, so the matcher goes on refusing to measure
with it.

---

## The matching queue was asking the wrong questions, and too many of them

Two hundred and ninety-one rows sat waiting for a person. Most of them should never have
been asked about.

**A coordinate we invented is not evidence of where a shop is.** When a chain publishes no
coordinate the row is stood at the centre of its town -- right for a map, wrong for a tape
measure, because every shop that chain has in that town then stands on the same spot: nought
metres from each other and nought metres from whichever old row is nearest the town centre.
Boyner publishes no coordinates at all, so its entire list arrived competing for one row per
town, and the loser of each fight went to the queue. A derived point is now treated as no
point, and the row is compared by name inside its own district, which is what we actually
know about it. Boyner's undecided rows went from five to two, and the two left are three
shops in Şişli that really do need looking at.

**The queue was a log, not a queue.** Every run records every row it read, and the undecided
ones were all shown, from every run there had ever been. A row a later run resolved -- because
the matcher improved, or because the shop it was confused with was merged away -- stayed
there forever, and the pile grew with every import whether or not anything new was in doubt.
It shows what each brand's most recent import could not decide. 291 became 178 without a
single decision being made.

---

## "Which of these have I liked" is now a question a page can ask

A page that is cached and served to everybody alike cannot carry a reader's own state in its
markup -- whoever's state was rendered into it is what the next reader sees. The store page
carried exactly one such thing after favourites were fixed: whether this reader had liked
each review on it, which meant the page could not be cached at all.

`GET /v1/me/likes?posts=…` answers it for up to fifty posts at once, so the page asks once
after it arrives rather than once per card.

---

## The admin store list says where each row came from

A thousand rows in the catalogue are left over from the provider the product no longer uses:
their name and address are still there and nothing stands behind them any more. Until now
the only way to find one was to query the database. The store list carries each row's origin
-- the chain's own published list, typed in here, from a visitor, or an unverified leftover
-- with the brand under it, and can be filtered down to any one of them. The filter is in the
address, so the list of leftovers can be bookmarked and handed to whoever is working through
it.

---

## Koçtaş, and two things its locator taught the adapter

Koçtaş sat in the registry as a tier-one chain with no locator and no shops. Its store
finder submits a plate code to an endpoint that answers with the province's shops as JSON --
128 of them, now in the catalogue.

**A plate code is written two ways and only one of them works.** The adapter substituted
"1" through "81"; Koçtaş's dropdown submits "01" through "81", and the wrong spelling
answers with an empty list rather than an error. Nine provinces -- every code beginning with
a zero -- would have come back silently empty and nobody would have seen it. The
configuration now says which spelling a publisher wants, `{province}` or `{province2}`,
instead of the adapter guessing.

**Koçtaş's own district field is not about the shop.** Ankamall is filed under Hamamözü,
which is in Amasya; Gordion under Haymana, sixty kilometres from where it stands. The field
is left unmapped and the district is read from the shop's coordinate, which is right. This
is what the registry is for: the publisher says what it is willing to say, and a field it
does not maintain is not evidence.

**The chain's name leads, and the town follows it.** A branch name that already begins with
the chain's own name had the town pushed in front of it -- "Etimesgut Koçtaş Ankara
Eryaman". The town now goes after the name the chain put there: "Koçtaş Etimesgut Ankara
Eryaman", and "Doğtaş Adana Exclusive - Çukurova" rather than "Adana Doğtaş Exclusive -
Çukurova".

---

## A shop with a coordinate is never a shop with no city

Three hundred and sixty-three shops stood in no city, and two and a half thousand in no
district. Every one of them had a coordinate. They answered no search for a place, sat under
no province in the catalogue, and were invisible to anyone browsing by city -- because the
place a shop is in was only ever read from what the publisher wrote, and Merinos and two
hundred other rows write nothing.

A row says where it is in two ways, and either can answer for the other. The name is still
asked first: it comes with a street address a person can read, and it is the field a chain
is least likely to get wrong. The point now answers what the name left empty -- the nearest
neighbourhood out of seventy thousand, not the nearest district centre, because a district
centre is the middle of its neighbourhoods and a large district reaches tens of kilometres
past it. Where the name gives a province but no district, the search is held inside that
province, so a bad coordinate can cost a district but never a province.

**A fifth of the neighbourhood table cannot say where anything is, and is now left out.**
Fifteen thousand of its sixty-nine thousand rows share a centre with a neighbourhood of a
different district -- where the real centre was not known something else was put there, and
one coordinate now stands for two places. Believing them put a shop on Lara, in Antalya, into
Manavgat, eighty kilometres away, because a Manavgat neighbourhood had been given a
Muratpaşa coordinate. Fifty thousand honest points remain, one every few hundred metres in
a town.

A point further than fifteen kilometres from every neighbourhood in the country is in open
country, at sea, or over a border, and is still told to say nothing.

Measured on the whole catalogue before it was written: 199 shops given a city they did not
have, 2,296 given a district they did not have, and 19 moved out of a district they were
already filed under -- each of those printed in full for a person to read, and each of them
a correction (an "Antalya Merkez" that is Muratpaşa, a Mardin "Merkez" that is Artuklu, a
Şanlıurfa shop filed under a district of Iğdır, and one shop whose district was the words
"yemek masası"). Afterwards: 14 shops without a city, 31 without a district, out of 8,589.

---

## The mark collector was throwing away marks

Forty-two percent of the catalogue -- Merinos, İstikbal, Bellona, Taç, Mondi, Doğtaş, nine
hundred shops between the first two alone -- showed an initial where a shop's sign belongs.
The collector reported "no usable mark on the page" for every one of them, and every one of
those pages had its mark plainly in the source. Four separate reasons, each measured before
it was changed:

**Both edges had to clear 96 pixels.** A wordmark is short. Doğtaş publishes its mark at
156x52 and Bellona at 468x72; both were thrown out for being 52 and 72 pixels tall. The test
is now on the longest edge, which is what a store card actually needs.

**The ratio cap was 4 and never did its job.** It was written to reject hero images, but a
hero is typically three to one -- the same shape as half the wordmarks in this trade. It
rejected Bellona at 6.5 to one and admitted every banner it was aimed at.

**Only the first match of each pattern was tried.** A page offers several images whose path
says "logo" and the first is routinely the wrong one: a vendor's badge, an app-store button.
Stopping there threw away the real mark sitting two matches later.

**Quoted attributes only.** Merinos serves its whole page minified, attributes unquoted, so
a pattern insisting on quotes found nothing at all.

Two capabilities were added for the same reason. A chain that draws its wordmark inline has
no file to fetch -- English Home taught that, and İstikbal turned out to be another -- so the
masthead's own `<svg>` is read, bounded by the drawing's declared size rather than its byte
length: the first attempt at this collected a 17-pixel chevron from five different chains.
And when a home page carries no mark, the brand's own store-locator page is tried, which is
where Merinos keeps its.

Coverage went from 42% of the catalogue to 95%. Mondi is what is left: its own domain does
not resolve, and we import its shops through the group's shared API.

**The importer now says when a brand has no mark**, because nothing tied "a brand was added"
to "its mark was fetched" and that is exactly how eight brands went a day without one.

**The collector is still not to be run blindly.** Loosening the patterns made it pick a
banner for English Home and a CMS image for Madame Coco, both of which already had better
marks on disk. It was run for the brands that had none. Look at what it writes.


## One dealer, two franchises, one shop

A shop in Antalya appears in Taç's published list and in Linens's, under nearly the same
company name at the same address. It is one shop that sells both, and the catalogue was
listing it twice -- telling a visitor to choose between two doors that are the same door.

"Two chains are two chains" was the rule, and it is right about English Home and Madame Coco
ten metres apart in a mall. What separates that case from this one is the name: a dealer
carries its own name into both lists, while two chains' own shops do not resemble each other
at all. So a near, same-named shop already held under another brand is this shop, and the
brand being imported is recorded in `store_carried_brands` rather than inserted again. The
shop's own details are left alone -- the second chain is not the authority on a dealer whose
sign is somebody else's, and overwriting here would make the shop flip spellings on every
import.

Four such shops existed and now carry two brands each. This also makes
`store_carried_brands` earn its place before anybody fills it by hand: it is what will
answer "somewhere in Antalya that sells Yataş".

**And an import that produces twins now takes itself back.** A net under a mistake already
made: the whole run is one transaction, so a check for two shops of one brand sharing a name
costs one query at the end and loses nothing when it fires. The cause of the last outbreak is
fixed; the next cause will be something nobody predicted either.


## Two shops at one doorway, and a word that does not exist

Both reported from the live site, both general rather than particular.

**A shop appeared twice.** "Çelik Mağazacılık Konyaaltı Bellona", a row left over from the
provider era, and our newly imported "Bellona - Antalya Çelik Centroom Konyaaltı" stand seven
metres apart and are plainly the same dealer. They score 0.28 by trigram similarity -- below
even the review band -- so the import inserted the second one without a word.

The evidence similarity misses is the one a person uses immediately: the shop already there
is signed with this chain's name. Two dealers of one chain do not share a doorway, so within
the merge radius that is now enough to merge, and the decision says so in those words. A row
the brand itself lists separately is still settled before this, so a chain with two branches
in one mall is unaffected.

Twenty-two such pairs existed. Fourteen were unambiguous -- exactly one branded shop within
sixty metres -- and were merged, three favourites moving to the surviving row and no review
touched. The other eight are further apart or have more than one candidate, and one of them
("English Home | Antalya The Land Of Legends AVM" beside "English Home - Isiklar CAD", 132 m)
is probably two real shops with one bad coordinate. Those are left for the matching queue
rather than guessed at.

**"Çelık" is not a word.** Turkish has two i's and a keyboard that makes the wrong capital
easy to type, so Bellona's own list says "ÇELİK CENTROOM ALTINTAŞ" on one row and "ÇELIK
CENTROOM KONYAALTI" on the next. Cased by the Turkish rules, the second becomes Çelık.

Vowel harmony was the obvious fix and was measured before being believed: across the
catalogue it repairs about thirty-five words and breaks about twenty-five, because Turkish
place and family names break harmony freely -- Kırşehir, Iğdır, Yılmaz and Ilgın all come out
wrong. Trading one kind of error for another is not a fix.

So the publisher decides instead. Within one brand's own list, a shouted word containing an
ASCII I is repaired only where that same brand writes the same word with İ somewhere else.
Nobody writes Kırşehir with a dot, so nothing invents one; Bellona writes MOBİLYA on most of
its rows, so the handful spelled MOBILYA are corrected to match -- 135 rows in that one case.


## Tier two, and four things I had wrongly given up on

Nine more chains: Çilek, Vivense, Dinarsu, Paşabahçe, Korkmaz, Merinos, Weltew, Pierre
Cardin Yatak and Alfemo.

Four of those I had already written off, and each write-off was my mistake rather than the
publisher's:

- **Çilek** I called broken because its endpoint answered with an empty array. It answers
  with an empty array to a province id that does not exist; I had passed 6 where the
  publisher's own id is 3663. The endpoint was fine.
- **Vivense** I called nameless because its showroom page carries a JavaScript array of
  points with no names. It also carries the shops themselves in the markup below, with their
  names, provinces, addresses and their own coordinates. I found the easy thing and stopped.
- **Paşabahçe** answered 403 once and I recorded it as blocked. It was transient.
- **Pierre Cardin Home** publishes no network, but Pierre Cardin Yatak does, at its own
  domain -- the same shops, under the name they actually trade as.

The lesson worth keeping: an empty answer and a refused one are both worth one more look
before they become a line in a document saying a chain cannot be read.

Three general capabilities came out of the work, none of them specific to a brand:

**POST locators, and two-step ones.** `method`/`body` carry a locator that answers only to a
POST -- WordPress puts every endpoint behind one address and tells them apart by a form
field. `over` fetches a list first and substitutes each of its values into the main request:
"ask which cities exist, then ask each city for its shops" is how a great many store finders
are built, and a fixed list of identifiers would be wrong the first time a publisher added a
city. Both the address and the body carry the substitutions, which is the bug I wrote first:
substituting only the address left the body asking for whatever the publisher defaults to,
and Çilek imported the same Kyiv shop 76 times without complaining.

**A 404 inside a fan-out is a missing city, not a broken locator.** Dinarsu's own site
answers 404 for some of the city identifiers it publishes. Abandoning the brand there lost
the other eighty. A single-address locator answering 404 still fails, because that means it
moved.

**robots.txt now follows RFC 9309 rather than something stricter.** A 4xx -- including the
403 several Turkish sites answer with -- means the file is unavailable, which means no
restrictions were stated, and the standard says a crawler may then proceed. Treating 403 as
a refusal was a rule stricter than any publisher had written, and it cost us chains that
serve their pages to us perfectly happily. A 5xx stays a refusal: the site is failing, and
hammering a failing site is what robots.txt exists to prevent.


## Two of tier two: Chakra and Emsan

5,133 stores across sixteen chains.

Emsan cost nothing at all: it is Karaca's group and runs Karaca's platform, so the mapping
already written read it unchanged. That is the third time this has happened -- Bellona with
İstikbal and Mondi, Doğtaş with Kelebek, Linens with Taç -- and it is worth saying plainly:
before writing a new locator, look at the page's scripts for a third-party host or a
familiar shape. Half the brands in this catalogue arrived on somebody else's mapping.

Chakra publishes each shop's point on the button that opens it in a map, which is a common
enough shape to be worth remembering: a chain with no coordinates in its markup often has
them in a maps link.


## Tier one, as far as it goes: IKEA and Özdilek in, four out with reasons

Fifteen of the twenty largest chains are now in the catalogue -- 5,048 stores -- and the
five that are not each have a reason recorded in `docs/brand-locators.md` rather than the
word "unmapped". Koçtaş is behind bot protection with a three-step form; Çilek's own
endpoint answers empty to browser and fetcher alike; Vivense publishes a hundred points and
no names, and a shop with no name is not a catalogue row; Deco Home's domain does not
resolve; Paşabahçe's store API refuses non-browser clients.

**Boyner Ev was removed on purpose**, and the registry now records that kind of decision
instead of losing it. "Boyner Ev" is a department inside Boyner's department stores, not a
store network; importing those branches would fill a home and living catalogue with clothing
shops, which is what the apparel filter exists to prevent. A deleted entry would have lost
the reasoning and invited the next person to add it back, so `active: false` with the reason
beside it is the honest record.

One defect came out of IKEA: Turkish pages write a decimal separator as a comma, and IKEA
publishes `data-latitude="39,89"`. The HTML locator's number reader split on the comma and
produced 39 -- a point in the Mediterranean, several hundred kilometres from Ankara. The
JSON side had handled this since English Home; the HTML side, being newer, had not.


## A locator that reads a page, and a store that says where its point came from

Some chains publish no endpoint at all. Özdilek prints its whole network into its own
markup -- 155 shops, each with a name, an address and a telephone -- and that list is as
complete as any JSON one. `kind: html` reads it: a pattern that isolates one shop's block,
and a pattern per field matched inside that block.

The configuration is regular expressions rather than CSS selectors on purpose. A selector
library is a second parser with its own opinions about broken markup, and these pages are
broken in ways nobody has catalogued; a pattern is checked by looking at what it actually
produced.

What such a page usually lacks is a coordinate, and that is the part worth being careful
about. Every one of those 155 shops is placed at the centre of the town its address names:
a few kilometres out rather than absent, which is the right trade -- but only if it is
visible. A catalogue that sorts results by distance and cannot say which of its distances
are measured and which are inferred is quietly lying to whoever reads it. The importer had
been deciding this per row all along and writing the reason into its run record, where
nothing on the request path could see it. `stores.location_from` puts it on the store.


## Three more chains, and a name that says what a shop is rather than how it is filed

Tepe Home, Yataş Bedding and Enza Home. Fourteen brands in the catalogue now, and 4,884
stores, none of which cost a provider request.

Each of the three needed a capability the adapter did not have, and each of those is a
general one rather than an entry in that brand's configuration:

**`extract_after`** reads a list a page carries as preloaded state rather than serving from
an endpoint. A regular expression cannot cut such a value out -- it contains brackets of its
own and RE2 will not count them -- so this names a literal marker and the adapter reads one
balanced JSON value after it, tracking strings so a bracket in an address does not end it.

**`urls`** fetches a list a publisher splits across more than one address, pooling the
results; repeats are deduplicated by identifier like any other.

**`require`** keeps only the rows whose published name matches, for a publisher that answers
with more than one chain in a single list. Yataş publishes its own network and Enza Home's
together across two addresses that mostly overlap, marking each row with the chain's code.
That marker is the publisher telling us which is which, which is better evidence than
anything we could infer: 42 Yataş Bedding, 60 Enza Home, no overlap.

**House words.** Yataş names a branch "ANK ORAN YB PRK SHW CAD", of which only "Oran" names
the shop; the rest is the chain's shorthand for which of its brands, whether there is
parking, and that it is a showroom. A list of codes would cover the chains somebody thought
of and fail silently for the rest, so the test is measured instead: a token appearing in at
least a quarter of one chain's own branch names cannot be what distinguishes its branches,
and if no other chain in the catalogue uses that token it is private vocabulary rather than
the trade's. That second measurement is what removes "SHW" and "PRK" while keeping "AVM" --
a word every chain in Turkey writes and which tells a shopper something real. A chain
imported into an empty catalogue has nothing to compare against and strips nothing.

Both sides of that comparison are folded by the same function, in Go rather than in SQL. A
second folding written in SQL is the kind of near-duplicate that agrees on every case
anybody tests and disagrees on the one that matters.


## The product vocabulary learns instead of being maintained

Measured against 108 ordinary Turkish words for things these shops sell -- written down by
asking "what would somebody type", not by reading the seed list -- the catalogue understood
68 and missed 40: çekyat, ayakkabılık, supla, parke, ankastre, fayans, and so on. Every one
of those reaches the model, waits about three seconds, and the next person to type the same
word waits again. A list maintained by hand fails exactly this way, and fails silently:
whatever nobody thinks to add is simply not understood.

Deriving the vocabulary from the catalogue was tried and does not work, for a reason worth
recording so nobody tries it again: our stores are chain branches now, and "English Home -
Mersin Yenişehir Forum AVM" contains no product word. Across every store name, only eight
words cleared a sixty-percent concentration threshold, and two of those were appliance
brands rather than products.

So the model teaches it, once per word. When the model does place a query, its own product
terms are written back to `product_terms` with `source='learned'`, and every later search
using that word is answered from our own table without a call. The first person to search
"ankastre" waits for it; nobody after them does.

The bar for writing an entry is deliberately high, because this table answers every future
search for every visitor: a wrong entry is a wrong answer repeated indefinitely, while a
refused one costs one more model call. Only the model's own product terms are kept, only
when it chose exactly one category -- two categories means it has not said which one the
word belongs to -- and never a chain's name, a word under three characters, or anything long
enough to be a sentence.


## Four ways a brand search failed, found by running one

Someone searching for a chain they can see from the window should get the branch nearest
them. Four separate things stopped that, all of them found by running the search rather than
reasoning about it.

**Turkish typed without its diacritics matched nothing at all.** "dogtas" met "Doğtaş"
nowhere: the name query lowercases and strips punctuation but leaves letters alone. The
catalogue has held a folded `compact_name` all along -- it is what the importer's matcher
compares on -- and the search simply never looked at it. It does now, against the same
folding applied to the query, which is the whole reason `textnorm.Compact` exists in one
place rather than two.

**A chain named inside a longer query was not recognised.** "doğtaş" worked and "doğtaş
mobilya" did not, because the catalogue lexicon only matched a chain against the entire
query. It now finds the longest chain name appearing as a run of whole words, and the rest
of the query still contributes its product words: "doğtaş mobilya" is Doğtaş and furniture,
"bellona yatak" is Bellona and beds.

**The nearest branch sorted below one 335 km away.** "kelebek mobilya" from Kadıköy answered
with Ankara first and the branch a kilometre away fourth, because `ts_rank` rewards a
shorter document and the Ankara branch's longer name scattered the two words further apart.
Rounding it, which is what the last attempt at this did, was not enough. It is gone from the
ordering: what a person typed appearing in a shop's name is the strong signal, and between
branches of one chain -- all equally the shop that was asked for -- the question is which
one can be reached. Phrase match, then distance, with `ts_rank` kept only as the tiebreak
for a search made before anyone has shared a location.

**Two runs of one import duplicated a chain.** Kelebek Mobilya was imported twice
concurrently, fifty-three seconds apart; both runs saw no existing shop, both inserted. An
import now takes a transaction-scoped advisory lock on the brand, so the second run says so
and stops. The two duplicate rows this produced carried no reviews or favourites and have
been removed.


## The panel's totals stopped being a guess

Two problems with the same shape, one after the other.

The catalogue tripled and the panel went on showing the figure it had before any of it
arrived. Those totals are a maintained counter, kept current by the events the product
raises as people use it, and an import raises none -- it writes stores straight into the
table. So every run left the counter further behind. An applied import now recounts when it
finishes: one query at the end of a run that took minutes.

And there is one store count again instead of two. The second card split the catalogue by
where its rows came from, which mattered while we were leaving a provider and stopped
mattering the moment we had left. Every row in the table is ours now; a panel that still
sorts them by origin only invites the question of which of the two numbers is the real one.


## Seven more chains, and the four defects that were keeping them out

The catalogue had three brands in it. It now has ten, and getting there turned up four
things that were wrong for every brand rather than for the ones being added.

**Shops had names that did not identify them.** Nine different shops were called
"English Home - Forum AVM" and five were called "Merkez CAD", because a chain names its
branches for staff who already know which city they are standing in. `DisplayName` now
decides what a shop is called, in the importer, where both the chain and the row's resolved
town are known: each part is added only when the published name is missing it, so
"Forum AVM" becomes "English Home - Mersin Yenişehir Forum AVM" while Madame Coco's
"Antalya Kepez Kültür Pop-Up Cadde" is left exactly as published and Doğtaş's own
"Doğtaş Exclusive - Adana Çukurova" is not made to say Doğtaş twice.

**A chain that publishes no id lost almost all of its shops.** Bellona publishes an `Id` for
a handful of branches and `null` for the other five hundred; every one of those was thrown
out as "no identifier". Where a publisher gives none, one is now derived from the row's own
folded name and its point rounded to about ten metres -- stable across runs, so a re-import
updates the shop instead of adding a second copy of it.

**A dry run would not say why it rejected anything.** The three checks that run before
matching counted their rejections and recorded nothing, so a run that threw out all 515 rows
printed "515 skipped" and left the reason to be guessed at. They are ordinary decisions now
and print like any other.

**A throttled host ended the import.** One 429 abandoned the brand and, with it, every
province after the one that failed -- silently, on a schedule nobody watches. Requests now
back off and retry, honouring `Retry-After`, for the statuses that are about pace; a 403 or
a 404 is still an answer and is not repeated.

Two general capabilities came out of the mappings themselves, both in the shared JSON
adapter rather than in any brand's entry: `{province}` in a locator URL is fetched once per
Turkish province, for the many chains whose store page asks per province (unpadded -- a
padded "07" answers with an empty list rather than an error, which would have lost Antalya
and eight other provinces in silence); and `coordinates` reads a point published as one
"lat, lon" string, which is as common as the separate pair.

Newly mapped: Bellona, İstikbal and Mondi through Erciyes Holding's shared dealer API;
Doğtaş and Kelebek Mobilya through the dealer panel they share; Linens and Taç through the
commerce platform they share. Seven brands, three mappings -- chains that share an owner or
a platform share a locator, which is worth looking for before writing a new one.

**English Home has a logo after all.** The collector looks for an image file and this one is
an inline `<svg>` in the page's own markup, so it reported none. It is now in the web
application beside the other marks.

## Search asks the model only what it cannot answer itself

Every search made an OpenAI call, and the latency of a search was mostly that call. Worse,
it was often paid for nothing. Measured against the live parser: `perde` and `yatak` -- the
two most ordinary searches this product gets -- came back from the model with a non-home
scope *and* search terms still filled in, a shape `Validate` rejects, so those searches
waited about three seconds, recorded `fallback=ai_unavailable`, and then used the
deterministic answer they had held all along. `english home` came back as `out_of_scope`,
which as a reading of two English words is not wrong; only the name-rescue path downstream
saved it.

The order is now inverted. `internal/search/lexicon.go` holds what this product knows about
its own catalogue -- the chains in `brands` and the product words in `product_terms`, both
refreshed every ten minutes -- and consults it after the deterministic parser. A query that
names one of our chains resolves to that chain; a query containing a product word resolves
to that word's category, longest match first so `yemek takımı` is tableware rather than
furniture by way of `yemek masası`. The model is then called only when neither pass placed
the query. It never overturns a refusal, and never rewrites categories the parser already
chose.

Against the live catalogue, every one of `perde`, `yatak`, `halı`, `gardırop`, `avize`,
`şifonyer`, `yemek masası`, `nevresim takımı`, `english home`, `madame coco` and
`mudo concept` is now answered without a model call. `merhaba` and `arkadaşıma hediye`
still reach it, which is what it is for.

`migrations/000027_product_terms_seed` seeds about eighty-eight Turkish product words with
`source='seed'`, so the table is useful before anybody has typed into it. Admin and user
entries land beside them and are distinguishable by that column.

An intent cache was planned alongside this and deliberately not built. Its value was in the
repeated queries -- and those are exactly the ones that no longer reach the model at all.
What remains is the long tail, where a cache mostly buys a write per search.

## Google is gone

The provider this product was built on top of no longer answers any request it makes.

- **Deleted:** the Places client, the sufficiency gate, the shadow sampler, `providerSearch`
  and the merge that folded provider results into ours, `materializePlaces` and the
  on-the-fly store imports, the lazy Place Details purchase on every store page, the photo
  proxy, `/v1/stores/resolve-external`, and the five commands that existed only to buy
  Place Details. With them went `GOOGLE_PLACES_API_KEY` and every `SEARCH_GATE_*` setting.
- **1,206 provider rows deleted. No store row was.** A review, a favourite and a rating
  belong to the shop, not to whoever first told us the shop existed; a catalogue that forgot
  the shop would take what people wrote with it. Those stores are marked unverified: still
  listed, still searchable, ranked behind anything a brand's own list confirms.
- The store photo is now an administrator's upload or the chain's own mark. There is no
  third source, and nothing on a store page is purchased.
- Opening hours are not published in the structured data any more. They came from the
  provider, and inventing them would be worse than their absence; the importer will supply
  them again from the chains themselves.
- A store page fetched its store twice per view -- once for the page, once for its metadata.
  Wrapped in React's request cache, so once.

**Reviews no longer require proof of being there.** Distance decides whether the review
carries the "verified visit" badge, not whether somebody may write one. Requiring proof made
the badge meaningful and the review rare: the only people who could write one were standing
in the shop with the site open, and a directory of empty stores proves nothing either.
Verified reviews are marked and sorted first, so the claim the badge makes is exactly as
strong as it was. A review written without a location has an *unknown* distance, which the
column now allows: zero would claim its writer stood in the shop.

**The published documents changed with it**, because they said store data comes from Google
Places and that a location-verified visitor is the only kind who may review. Both were true
this morning. The KVKK transfer table and the privacy policy's recipients table each lost a
foreign recipient, which is a reduction in what we declare rather than an addition.

## A chain's own mark, instead of a photograph we have to buy

Store imagery stops being something purchased per store and becomes one file per chain --
the model Trustpilot uses, and the one that survives Google leaving.

- A brand mark is public, identifies the brand, and does not change, so it is not user
  media: no owner row, no signed URL, no storage bill. `cmd/brand-logos` collects them
  through the same robots-respecting fetcher an import uses, and they are committed beside
  the web application's other static assets. Nineteen of thirty-five brands have one; the
  rest either publish no usable mark or do not answer us, and their stores keep the initial
  letter they already showed.
- The photo a store shows is now: an administrator's upload, then its chain's mark, then a
  provider photograph for as long as we still hold one.
- A mark is drawn contained on a plain ground rather than cropped to fill, because a logo
  rendered like a photograph reads as a mistake.
- **The tool's output has to be looked at.** A site's markup is not always honest about
  which image is its mark: English Home's first image whose path says "logo" is a
  photograph of a phone, and the one beside it is a pair of app-store badges. Size and
  shape tests throw out favicons and banners -- twenty of the first pass were 32-pixel
  icons -- and a wrong-but-plausible picture still gets through. English Home therefore has
  no mark; a screenshot would have been worse than none.

## Three more chains, and four things a brand's own list gets wrong about itself

Madame Coco (673 shops) and Mudo Concept (73) join English Home (305): **1,051 verified
stores, and every published id maps to exactly one of them.** Both new chains run on the
same commerce platform, so both were a block of configuration rather than any code.

`cmd/catalog -probe` is how a brand gets mapped now. It fetches the likely locator paths
through the same robots-respecting fetcher an import uses, reads what comes back for the
marks of a store list, and reports the endpoints a page's own scripts call. It found both
chains in one pass; the other seventeen tier-one brands publish nothing at the standard
paths and still need a person to look once.

What the imports taught, each of it fixed as a rule rather than a patch:

- **A chain's internal city code is not part of a shop's name.** English Home publishes
  "ANK ACITY AVM"; the "ANK" is Ankara in their warehouse shorthand and means nothing to
  anybody looking for the shop. It comes off by a general test against the row's own
  province -- a short shouted first token whose letters appear in order inside the province
  name is that province abbreviated. It catches ANK in Ankara, DYR in Diyarbakır, GTP in
  Gaziantep and BLK in Balıkesir, and leaves "LARA" alone, which is not a subsequence of
  "Antalya". Names now read "English Home - Acity AVM".
- **A store list is not a promise that everything on it belongs here.** Mudo publishes its
  clothing departments beside its homeware ones -- "Akmerkez Home" and "Akmerkez Giyim" at
  the same address -- and a directory of home and living stores that lists a clothes shop is
  wrong about itself. Fifty-four were skipped on the trade's own department words.
- **A chain's foreign branches are not Turkish shops.** Madame Coco publishes in Almaty,
  Amman and Astana, and the administrative table cheerfully matched Tashkent to Taşkent, a
  district of Konya. Forty rows were being placed in Turkey; a point outside the country now
  drops the row rather than relocating it. Seventeen legacy rows in Germany, Belgium and the
  Netherlands went with them.
- **Publishers nest.** Madame Coco's township is an object holding the district's name and,
  under it, the city's. A mapping that could only read flat fields read nothing, and the
  address fallback then placed a shop in İstanbul Maltepe at Hani, in Diyarbakır, because a
  word in its address happened to be a district somewhere. Field paths are dotted now, and
  the fallback no longer guesses a district from a stray word: a wrong district is worse than
  a missing one, because the catalogue is grouped and searched by it.

Two brands are two brands: a store already confirmed as one chain's is never merged into
another's, however close they stand and however much of the shopping centre's name they
share. That alone took seventeen rows out of the review queue on the first Madame Coco run.

## A brand search near Kadıköy answered with Diyarbakır

Found the moment the catalogue held three hundred shops of one chain, and it had been true
all along with too few rows to notice.

Candidate selection ordered by raw `ts_rank` before distance. That score rewards a shorter
document, so a chain's branch names sorted by how many words their code has -- "DYR 75.CAD"
above "YLV STAR AVM" above "IST AND MALTEPE PIAZZA AVM" -- and the thirty-row limit threw
away all seventy-nine İstanbul shops before the ranker that orders by distance ever saw
them. The score is now rounded to one decimal: a name matching both words still beats one
matching neither, and "this sign is longer" no longer beats "this shop is two kilometres
away". Searching "english home" from Kadıköy used to answer at 41 km; it now answers at 2.

It had to be fixed in two places, and finding the second one is the point. Rounding the
score in the ordinary catalogue query changed nothing on the live site, because a brand name
does not take that path: the model reads "english home" as unclear, the name rescue picks it
up, and that runs a different query with its own copy of the same ORDER BY. The response
still reports the scope as home-living, because the rescue rewrites it on the way out --
which is exactly why the first fix looked like it should have worked.

## The catalogue starts being ours: brands, imports, and a matcher that will not make twins

Second step of removing Google Places. The chains publish their own store lists; this reads
them, and every decision it makes about a row is written down.

- `brands` is a registry, loaded from a committed file (`internal/catalog/data/brands.yaml`)
  into a table. Thirty-five Turkish home and living chains are listed with their categories
  and tier; one of them, English Home, has its locator mapped. Adding a chain is a block in
  that file, not a Go file: most locators are a JSON endpoint the brand's own page already
  calls, so a config-driven adapter covers them.
- Fetching obeys `robots.txt` per host, one request a second, identifying itself as
  `BosaGezmeBot`. This is applied as a rule rather than checked by hand: English Home's file
  allows the list endpoint this reads and forbids the per-store pages beside it, and that is
  the kind of distinction twenty adapters would each get wrong once.
- Every run is recorded in `store_import_runs` / `store_import_records`: what was fetched,
  what was decided for each row, and why. Dry run is the default, and the dry run is the
  point -- the matcher's verdicts are what a person reads before anything is written.
- **First English Home import: 305 shops, 13 of them merged onto rows we already held.**
  No duplicates: 305 published ids, 305 stores, one to one. The four same-brand pairs left
  standing within 150 m of each other are adjacent shopping centres the brand lists
  separately.
- The matcher needed three rules that a similarity threshold alone does not give, and each
  came from a row it got wrong first:
  - **Containment.** "englishhome" inside "englishhomeantlaracad" scores 0.42 by trigram and
    is obviously the same shop 57 m away. One name holding the other counts as identity when
    it stands beside proximity.
  - **The brand is the authority on its own shops.** "Ayvalık 1" and "Ayvalık 2" are 229 m
    apart and share four fifths of their name; no threshold separates them. A candidate
    already carrying a different id from the same list is a different shop, full stop.
  - **One store per run.** A row already matched by an earlier row cannot be matched again,
    or two branches either side of one vague old row collapse into a single shop.
- A published coordinate that contradicts the published place is replaced by that place's
  centre. English Home puts its Çeşme shop in Trabzon, 1,119 km away, and its Ünye shop in
  Bodrum. A shop at the wrong end of the country is worse than one with no point: it answers
  searches near a city it is not in. Measured against the province and not the district,
  because a large district legitimately reaches 50 km beyond its own middle -- the tighter
  test moved four correct shops for every two wrong ones.
- `cmd/normalize-legacy` folds the names of everything already in the catalogue before any
  of this runs. Rows imported from a provider carry that provider's spelling, and until they
  are folded the matcher is comparing against text it cannot read -- it would have created a
  second copy of every chain store we already hold. 1,057 rows, 153 names retitled.
- Turkish casing is decided by the name, not by the field: a name carrying ç, ğ, ı, İ, ö, ş
  or ü is cased as Turkish and any other as plain Latin. Word by word looks better and is
  worse -- across the live catalogue, 43 of the 49 words the two rules disagree about are
  Turkish written without diacritics (HALI, TASARIM, KONYAALTI) and want the dotless ı.
- Also created, with no interface yet, because search will be built on them and adding them
  later costs far more: `store_carried_brands` (which brands a shop sells -- the question
  "a shop in Antalya carrying Yataş" cannot be answered from a shop's own name),
  `store_attributes`, and `product_terms`.

## The location picker is ours: Turkey's own places, no provider behind them

First step of removing Google Places from this product entirely. Choosing *where* to search
was the part of that nobody had counted: `/v1/locations/search` was Places Autocomplete,
`/v1/locations/resolve` was Place Details, and a search cannot run without the coordinates
resolve returns. Removing Google without replacing this would not have thinned the product,
it would have stopped it.

- New table `tr_locations`: 81 provinces, 973 districts and 69,262 neighbourhoods, each with
  a point. The data ships inside the binary (about a megabyte, gzipped) and is loaded by
  `make seed-locations`. Nothing is fetched at request time and no key is involved, so this
  part of the product can no longer be unavailable or cost anything.
- Both endpoints keep their shape. `provider` is now `bosagezme` and `place_id` carries our
  own row id; `attributions` is empty because there is nobody left to credit. A prediction
  still carries no coordinates the client may choose — the browser returns an id and the
  point is read here, which was a property of the old picker worth keeping.
- Names are stored folded as well as printed, so an index answers `unca` for `Uncalı` and
  `kadikoy` for `Kadıköy`. The two foldings moved to `internal/textnorm`, because the
  catalogue importer that follows has to fold a Turkish name exactly the way search does,
  and a second implementation that disagreed by one character would make one place into two.
- Ordering is the design: a name starting with what was typed beats one merely containing
  it, a province beats a district beats a neighbourhood, and where the visitor is standing
  breaks the remaining ties — there are several hundred neighbourhoods called some form of
  "Cumhuriyet". Two-letter queries are answered from the front of names only, or "an" would
  sort every place in Turkey containing those letters.
- The generated dataset is not trusted point by point. A district's position is the *median*
  of its neighbourhoods, so one mistyped coordinate cannot move it, and a neighbourhood more
  than 80 km from that median is dropped rather than published — about 3% of the published
  rows. Turkish casing is applied with Turkish rules; lowercasing `BALIKESİR` the ordinary
  way produces `Balikesir`, which is a different word.
- Removed with it: `PlaceEssentials` and `Autocomplete` from the Places client, the
  `AutocompleteProvider` and `PlaceEssentialsProvider` interfaces, and the location-anchor
  filtering that existed only to tell a provider's shops from its administrative areas.

## A Google sign-in now brings its account picture, and a review carries its eight scores

- The Google ID token has always contained the account picture and it was thrown away, so
  every profile square held an initial. It now fills an empty avatar on sign-in, and only an
  empty one: a picture chosen here belongs to the person who chose it. Only https URLs are
  accepted.
- Reviews returned for a store or a profile now include the eight criterion scores they were
  built from, so a reader can see what a four out of five was made of. Reviews written before
  the criteria existed carry nothing rather than a row of zeros.

## Search history belongs to whoever searched, account or not

- Reading and clearing search history required a signed-in account. Browsing is anonymous by
  design and every search is already recorded against the visitor's session, so an anonymous
  visitor was told they had never searched anything. A visitor session now owns its own
  history exactly as an account owns its own; a signed-in account is always the owner when
  there is one, so the two can never be mixed.

## The favourites list never said whether the viewer had reviewed a store

- The flag exists on the store DTO and the detail query fills it in, but the favourites
  query did not select it, so it defaulted to false for every row. Anything counting on it
  -- such as "saved but not yet reviewed" -- was counting the whole list.

## The catalogue rescue is for text we could not place, not for text we refused

- Before answering "we did not understand you", the catalogue is searched by name -- that is
  how "güney antalya" finds the shop registered under that name. It was running for refused
  queries too, so "halı saha" was vetoed and then answered anyway with 28 carpet shops whose
  signs carry "halı".
- The two cases look identical downstream and are now told apart at the source: whether *we*
  vetoed the request, or the model merely failed to read it as one. Only our own veto stops
  the rescue. Distinguishing them by the final scope alone is what broke "güney antalya" in
  a first attempt at this -- the model calls that address out of scope too.

## A query naming another trade is refused, as a sign naming one already was

- "Halı saha" was read as a carpet search because it contains the word for carpet -- the same
  mistake the sign test had already learned about the compound, made again on the request
  side. The other-trade words now veto a query as well as a shop name.
- The food words are deliberately not applied to queries. "Yemek masası" is a dining table
  and "kahvaltı takımı" is a breakfast set; both begin with a word that names a food
  business. A sign that begins "Yemek" is a restaurant; a sentence that begins "yemek" is
  usually about furniture.

## A vetoed query is no longer answered by the catalogue's own signs

- Searching "depolama" still returned warehouses. The veto worked: the query was classified
  out of scope and the model was never asked. What answered it was the name-rescue path,
  which searches our own catalogue before telling somebody we did not understand them, and
  which filtered its matches through the service-business test only. Warehouses are not
  service businesses, so every catalogue row whose sign carries the word came straight back.
- The exclusion vocabulary now lives in one predicate covering services, other trades and
  warehousing, and both doors into an answer -- the provider's results and a catalogue name
  match -- are held to it. Two lists that were supposed to say the same thing had drifted;
  now there is one.
- The storage words are shared between the query veto and the sign test rather than written
  out twice, so a word added to one is a word added to both.
- The warehouses remain in the catalogue and are simply no longer reachable through search.
  Retiring the rows themselves is a separate, destructive decision and is not taken here.

## Welcome email now matches the current review journey

- The welcome message now describes location-ordered store discovery, proximity
  verification, eight-criterion reviews, and contribution levels instead of the retired
  free-text, photo, and favourites journey.
- Equivalent copy ships in all four supported locales so the email does not describe a
  different product depending on account language.

## Explicitly excluded trades can no longer be reintroduced by intent enrichment

- Deterministic out-of-scope terms such as warehouse/storage and repair services now veto
  model enrichment. This prevents a broad interpretation such as home organisation from
  turning a plainly excluded warehouse query back into a home-store search.
- Explicit exclusions skip the intent-model request entirely, reducing latency and cost
  for requests the product already understands; unclear wording still receives the normal
  language-model interpretation.
- The home-and-living-only response is covered in all four supported locales.

## Catalogue-first search and detail caching now prevent repeat Places spend

- Local-first search is now the production default rather than an opt-in. A named store
  already found in PostgreSQL is a complete local answer and does not call Google even
  when the surrounding catalogue is below the generic result or coverage thresholds.
  A missing named store and an insufficient generic discovery search still reach Google,
  preserving catalogue discovery instead of blindly suppressing it.
- Paid shadow comparisons now default to zero and must be enabled deliberately for a
  bounded quality study. The six-hour query/location cache continues to collapse
  identical provider-backed searches.
- Google Text Search no longer asks for or exposes photograph metadata, and search lists
  render no thumbnails. Removing that field does not lower Text Search below the Pro SKU
  because the result name, address and coordinates already require Pro; it prevents the
  separate Place Photos request that each visible Google thumbnail could otherwise trigger.
  Store detail keeps the required image priority: administrator-owned media first, then the
  persisted Google photo reference, then the no-photo state.
- Store detail continues to fetch a missing Google record once under a cross-instance
  advisory lock, persist the complete answer and mark even empty optional fields as
  fetched. Once that marker exists, later detail reads use PostgreSQL and never call Place
  Details again.

## Result lists no longer buy or expose store-detail data

- Google Text Search now asks only for the identity, address, coordinates, provider types,
  and business status needed to classify and render a result. Rating/count, opening
  hours/timezone, phone and website are absent from both the field mask and list DTO. This
  reduces the data collected on result pages; the later list-photo removal above prevents
  the separately billed media fan-out.
- The first read of a Google-backed store detail fetches the complete Place Details record,
  persists it, and records that even an empty optional answer was fetched. Every later read
  uses PostgreSQL. A transaction advisory lock serializes the first read across application
  instances, so concurrent taps do not buy the same detail twice. Existing stored provider
  data is retained and legacy enriched rows are marked by migration rather than refetched.
- Cheap search refreshes merge into a stored provider record instead of replacing it, so a
  later search cannot erase rating/contact/hour data already bought for the detail page.
  The search-cache key is versioned so a pre-deploy rich response cannot leak those fields
  back into a list during its remaining six-hour lifetime.
- Resolving a manually selected discovery location now uses an Essentials-only Place
  Details mask. Store ratings, contact data, hours and photos were never read by that flow,
  so buying them there was pure waste.

## Store-name search ignores punctuation and service words keep workshops out

- Store-name matching now treats punctuation as presentation rather than identity, so a
  person typing `Willas` can find a catalogue/provider name written as `Willa’s` without a
  hand-maintained brand alias.
- The trade-wide word `tamirhane` now identifies a repair workshop on import, deterministic
  intent parsing, and the catalogue-name fallback. A data migration removes stale retail
  category links from existing workshop rows without making a paid Places request.
- Warehouse/depot requests no longer pass through the retired `storage` category. Cabinets
  remain ordinary furniture products; warehousing remains outside this retail product.
- The structured AI schema now accepts the already-supported major- and small-appliance
  categories and no longer advertises the retired storage category.

## Store detail exposes comparable review-criteria averages

- Store detail now returns the mean of each of the eight first-hand review criteria,
  together with the number of complete criteria reviews behind those means. Legacy
  reviews without criteria remain part of the overall community score but do not dilute
  a criterion average with zero.
- Out-of-domain search guidance now uses the product-owner-approved concise home-and-living
  message and no longer returns rotating query examples. Clients can offer the canonical
  category list as the recovery path instead.
- The private profile now includes the next contribution level and the exact number of
  reviews remaining. The threshold table stays server-owned, so web and mobile cannot
  silently disagree as levels evolve.

## A partial local result page no longer prevents provider discovery

- The local-first gate considered eight matching stores enough to skip Google even though
  the response can show thirty. Real searches demonstrated the cost: a carpet search in
  Izmir stopped at 15 local results after the provider had previously returned 18, and a
  home-textile search stopped at 22 until a name search happened to import the missing
  store.
- A category search now stays eligible for provider discovery until all thirty local
  candidate slots are filled. This deliberately favours catalogue completeness over the
  lower call count; the existing relevance, coverage, explicit-name and cache safeguards
  are unchanged.

## Search no longer mistakes services for the products they handle

- A carpet cleaner contains the word “halı”, and a photography studio can share a retail
  brand name. Both could therefore inherit a product category and survive name search even
  when their business is not a shop. Laundry/cleaning provider types, photography types,
  and the trade-wide phrases “halı yıkama” and “photo/fotoğraf studio” now veto that result.
- Existing catalogue rows are corrected from provider types and names already stored in
  the database. No Places request is made, and no store-specific exception was introduced.
- The catalogue candidate stage now supplies all thirty available result slots and applies
  distance before category breadth. It previously stopped at twenty and sorted broad
  multi-category shops behind specialist shops before the final ranker, so nearby catalog
  stores with an exact category never reached the response at all.
- Provider text search is treated as a candidate source, not a category verdict. A result
  outside every requested category is now dropped unless it is an explicit store-name hit
  or a clearly labelled promoted store. White-goods searches request white goods only;
  white-goods stores still carry the one-way small-appliance classification, so they remain
  valid answers when the shopper asks for small appliances without making the reverse true.
- `storage` is inactive as a browsable category. Its reference row remains so historic
  search reporting keeps the same dimension and rollback is safe.
- Deleting a fractional criteria-average review no longer scans its numeric rating into an
  integer merely to discard it. That mismatch made the delete endpoint fail for ordinary
  eight-criterion averages such as 4.125.

---

## The rate limit counted the web server, not the person

- Reported as store pages failing when opened from a results list and working after a
  refresh. Two faults meeting.
- Every request reaches this service from the web server, so the limiter -- which keys on
  the remote address -- put the entire product in one bucket: 180 a minute, burst 40,
  shared by everybody. It also ran before authentication, so the per-account key it already
  had could never apply.
- A results page then began prefetching every store it listed: two dozen server renders,
  each a read of this service, arriving at once from that one address. The burst emptied,
  and the stores the page had just listed came back 429 -- which the web app turned into
  "this store is no longer in our list" and cached.
- The limit is now counted against the person: their account when signed in, their browsing
  session when not, the address only when there is neither. Identity is read before the
  count; the account lookup stays behind it, because that one touches the database.

---

## Eighty businesses that were never home and living leave the catalogue

- Kebab houses, five-a-side football pitches, hairdressers, spas, a language school, an LPG
  dealer, a fruit-and-vegetable wholesale market, and the repair services. The catalogue
  collected them because anything a search turned up was kept.
- Two pitches survived the first pass **classified as carpet shops**, which is the trap the
  classifier already warned about in a comment: Google typed one "playground" and the other
  nothing at all, so the sign decided, and the sign says "halı". The compound is the fix.
  "Halı" alone is a carpet and stays one; "halı saha" is never anything but a football pitch,
  in any city in the country -- so nobody has to add the next pitch by name.
- **Why having no category was not enough.** A store is found by its name *or* by its
  category -- the two are an OR, not an AND. Taking the categories away closes one of the
  two doors and lowers the ranking; the name still matches. Measured: a search for "ayna"
  returned eight stores and five of them were hairdressers and a spa, all uncategorised.
  "Sofra" does the same for kebab houses, because it is a real product word.
- The verdict is the classifier's, from Google's own types, not a list anybody typed out.
  Nothing that carries a review, a favourite or a verified visit was touched -- there were
  none.
- **Soft-deleted, not destroyed.** `deleted_at` is set; clearing it brings a store back with
  its photo, rating and history intact. The full list is in
  `docs/retired-stores-2026-09-02.txt` so the decision can be read and reversed by name.

### What this costs in search

- Eighty store URLs stop being served and leave the sitemap. They were indexable
  pages with a name, an address and a map, and whatever standing they had goes with them.
- That is the point of the change rather than a side effect -- a kebab house ranking for
  home and living queries is the damage, not the benefit -- but it is a real loss of pages
  and it is stated here so it is not discovered later.
- One entry is worth a second look before this is treated as settled: "Antalya Yıldızlar
  Endüstriyel Day. Türk. Mal. Tic." is retired because Google types it `food`. The name
  says durable consumer goods. If it is a white-goods trader rather than a catering
  supplier, clearing its `deleted_at` is the whole fix.

---

## White goods had a category of their own and nothing could ever enter it

- Searching "beyaz eşya" searched the general household bucket: 214 stores that also hold
  hardware shops, paint shops, tile shops and builders' merchants. Of 25 stores with "beyaz
  eşya" in their own name, 24 were filed there and one carried anything more specific.
- `major_appliances` existed and was empty, because the only way in was Google's
  `appliance_store` type -- which appears in none of the 969 Google records we hold. "Çağrı
  Beyaz Eşya Dünyası" arrives typed only as "store, point_of_interest, establishment": the
  provider does not say what it sells. So the evidence has to be the shop's own sign, which
  is the trade's own words and therefore holds in a city nobody has visited.
- A white-goods dealer also sells kettles and blenders, so the category carries
  `small_appliances` with it -- one way only. A shop that sells kettles is not thereby
  selling washing machines.
- Backfilled from names and types already stored, with no provider call:
  `small_appliances` 14 → 38 stores, `major_appliances` 0 → 19.
- "Tamir bakım" and "bakım onarım" join the repair words. The two-word forms are safe where
  bare "tamir" is not: they are the trade's name for a maintenance contract, and no shop
  selling goods describes itself with either. "Antalya beyaz eşya ve klima tamir bakım
  onarım montaj" was still being read as a shop.
- `align-categories` gained `-keep-all`. Retiring a store and giving it a category are two
  decisions and they were welded together; whoever runs it can now take one and leave the
  other. The report still says what would have been retired.

---

## A review is eight scores now

- Eight criteria -- product availability, value, layout, staff attention, staff knowledge,
  checkout speed, returns, cleanliness -- each one to five, all of them required together.
  Half of them filled in is not a shorter review; it is a different measurement, and
  averaging it against a complete one would quietly make the two mean the same thing.
- The store's overall rating is derived from the eight rather than asked for separately. A
  ninth score that is meant to summarise the other eight is a ninth thing to disagree with.
- Review text is no longer required. Nothing existing was deleted: every published review
  keeps its text and photographs in the database, and the web simply stops showing them.
  Reversible in one line if the decision changes.
- The eight columns are nullable because every review published before today exists without
  them and there is no honest value to invent.
- **The phone app is unchanged and keeps working.** It still posts a paragraph and one star,
  and a request that carries no criteria is held to the contract it was written against
  rather than rejected. Its reviews arrive without the eight, which is what the nullable
  columns are for.

---

## A search for shops that sell white goods was answering with repairmen

- Twenty-six stores in the catalogue call themselves "servis" or "tamirci" -- "Beyaz Eşya
  Servisi", "Aksoy Teknik Servis", "Şanlıurfa Beyaz Eşya Tamircisi". They are repairmen.
  Nobody visits them to buy a washing machine, and they were coming back under searches for
  the shops that sell one.
- Both words are now read the way "tadilat" and "mimarlık" already were: a name that says
  labour is not a shop. Bare "tamir" is deliberately left out -- a maker often repairs what
  it makes, and "YIKILMAZ MOBİLYA ... imalatı ve tamir" is a furniture shop with a workshop.
- What is inside brackets no longer counts. A shop that is also an authorised repair point
  for the brands it stocks writes that in brackets after its own name -- "Akay Ev Aletleri,
  Antalya (… Braun Yetkili Servis)" -- while the repairman puts it in the name itself. That
  is how the brand-authorisation note is written everywhere, so the distinction travels
  rather than naming any particular shop.
- Tested against the real names, both the six that must be excluded and the six that must
  not be.

---

## Small-appliance searches no longer fall into home accessories

- “Küçük ev aletleri” was missing from the product vocabulary even though the catalogue
  already has a dedicated small-appliances category. The search therefore had no precise
  category signal and could rank decoration and accessory stores.
- The trade phrase and its shipped-language equivalents now resolve directly to small
  appliances. The rule is category-wide and uses no store or brand exception.

## Public transport landmarks can be used as a manual search origin

- Google classifies ferry, rail, metro and bus terminals as establishments and points of
  interest. The location picker offered these recognisable landmarks but rejected the
  selection when it resolved the provider record, so a person could not search around a
  place such as a ferry terminal.
- Provider-typed public transport landmarks are now valid search anchors after their place
  ID and coordinates are re-fetched and verified. Ordinary shops remain excluded, and a
  manually selected origin still provides no evidence that somebody visited a store.

## Home discovery trends are live without exposing individual searches

- A public read endpoint now returns the most searched cities from the rolling 30-day
  window, capped at ten and withheld until a city has at least three completed searches.
  It exposes no district, coordinate, query, visitor, or user data.
- The frontend contract now groups this city signal with the existing monthly store
  highlights and category search totals so Home can show all three without invented data.

---

## Catalogue stores are an explicit editorial state

- Stores can now be marked as catalogue entries by an administrator without making them
  promoted or changing search order. The operation is audited independently from premium
  placement and category editing.
- Store detail and hybrid search contracts expose the marker so clients can present the
  same editorial state everywhere instead of inferring it from a name, category, or image.
- The schema defaults existing and newly imported stores to standard, making catalogue
  membership an intentional operator decision.

---

## Product feedback can receive a private in-app answer

- Feedback from a signed-in account now appears under that account's private messages,
  together with any administrator reply. Pending messages stay visible, so “sent” does not
  become an unexplained disappearance while somebody waits for an answer.
- The operator feedback queue can answer an owned message and close it atomically. The
  action is audited without copying the private reply into audit metadata.
- Anonymous feedback remains anonymous and cannot be claimed later by matching an email
  address; account ownership, not contact text, is the boundary for reading a message.
- Account deletion removes owned feedback and replies with the rest of the private profile;
  signing in again starts with an empty message history, like every other purged section.

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
