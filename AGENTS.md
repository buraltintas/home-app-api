# Workspace design workflow

For frontend or product-design work in the sibling `ui` and `mobile` repositories, use `.agents/skills/impeccable/SKILL.md` as the general quality foundation and `.agents/skills/home-app-design/SKILL.md` as the Boşa Gezme!-specific authority. The product-specific authority wins when the two conflict. Do not apply UI-only guidance by changing backend application logic.

Keep repository documentation and developer-facing explanations in English.

## Rules are general, or they are not rules

A rule that names a shop is not a rule, it is a patch. This product covers every city in
Turkey and every store in them; nobody can maintain a list of the ones that need special
handling, and the attempt fails quietly — the shops nobody thought to add just stay broken.

So a classification or ranking rule has to hold for a store nobody has looked at, in a city
nobody has visited. In practice that means, in order of preference:

1. **The provider's own data.** Google types every business in the country. It is imperfect
   and occasionally wrong, but it is complete, and complete beats accurate-for-the-twelve-
   shops-somebody-checked.
2. **Words the whole trade uses.** "Mefruşat", "tadilat", "uyku merkezi", "züccaciye" —
   every business of that kind in Turkey uses them, so a rule reading them travels.
3. **Nothing else.** Not a brand list, not a per-store exception, not a fix for the example
   in the bug report.

This was learned by doing it wrong. A list of chain names looked harmless and cost us twice:
"Taç" is a home textile chain and also a language school, a clinic, a nursery and a housing
development, and the list had to keep growing to stay useful. It was removed once the
provider's types made it redundant — measured, not assumed.

When a report names one shop, fix the class of problem it belongs to. Then check what else
in the catalogue is in that class, because there is always more than the one reported: two
renovation firms in a bug report turned out to be seventeen.

## The provider bills by the question, not by the answer

Every call to Google Places costs money. There is no free allowance worth planning around,
and a backfill that "only" touches the catalogue once is still one paid call per store.

This was learned by spending about a thousand lira in a day. Four separate maintenance
commands each walked the same catalogue and each asked the provider the same question about
the same store: categories, then website and phone, then photographs, then closure status.
Roughly 1,490 calls where a single pass would have made about 600. Nothing was wrong with
any one command. The waste was in running them one after another instead of together.

So:

- **Ask once, take everything.** The field mask is billed by its most expensive field, and a
  request for one such field costs what a request for all of them costs. Never split one
  pass over a catalogue into several.
- **Say the number before spending it.** A command that will call the provider states how
  many calls and roughly what that costs, and waits for a person to agree. "It is probably
  within some free tier" is not an estimate; it is a hope, and this one was wrong.
- **Cache every answer to disk.** A dry run and the apply that follows it must share one
  fetch, and an interrupted run must resume rather than start the bill again.
- **Never call the provider to satisfy curiosity.** Checking one store by hand is a paid
  call too. The catalogue already holds what was fetched; read that first.
- **Do not ask the same question twice.** Provider answers to an identical query from the
  same area are cached for a few hours. Two thirds of the searches on record repeated a
  query already asked nearby, and every repeat was paid for again.
- **Do not save money by not looking -- and a result count is not looking.** Skipping the
  provider whenever the catalogue merely looked full enough was measured before it was
  written: in a quarter of the searches that would have skipped, the provider was bringing
  back a store we did not have, and on our own ranking the stores only it knows about score
  a median 114 against 70 for the ones we hold. When the provider adds something it tends
  to add something good. A search may be answered locally only when four things hold at
  once -- enough results, results that are actually about what was asked, enough catalogue
  held nearby for their absence to mean anything, and no store named outright -- and the
  decision has to be checked against reality by asking the provider anyway on a small
  sample, off the request path, with nothing imported from it. A measurement that changes
  what it measures is not a measurement.
- **The measured ceiling is not the target.** Three quarters of past searches ended with
  the provider adding nothing; that is the most a perfect gate could ever have saved, not
  what a real one should reach on its first day. Thresholds start conservative and move on
  evidence. Loosening a threshold to hit a number is spending the product to flatter a
  metric.
- **Asking less often also refreshes less often.** What the provider told us has a shelf
  life -- photographs may only be shown while the record is recent -- and today a record is
  refreshed as a side effect of being asked about. Any change that reduces provider calls
  therefore quietly ages the catalogue too. Say so when making one, and give the freshness
  its own schedule rather than leaving it to depend on how often somebody happens to search.
- **Do not schedule it.** A store people actually search for is refreshed by the search
  itself, which is already paid for. A recurring backfill pays a second time for data that
  arrives free.

## Keep the log

Every change that a person would want explained later goes in `docs/CHANGELOG.md`, newest
first, in the same commit as the change itself. Not a list of files touched — what changed,
and why it was worth changing. A defect entry says what was actually broken: "fixed the
search" tells the next person nothing, "the same query returned different stores because
the intent classifier ran at the default temperature" tells them everything.

The other documents are load-bearing too, and a stale one is worse than a missing one:

- `docs/architecture.md` — how the pieces fit. A new service, boundary or background job
  belongs here the day it ships.
- `docs/frontend-handoff.md` — every DTO and endpoint the clients read. A field added or
  renamed without updating this is a field the clients will get wrong.
- `docs/reporting.md` — what each metric counts, so nobody has to reverse-engineer a number
  from SQL before trusting it.
- `PRODUCT.md` — what the product is and refuses to be.
- `AGENTS.md` (this file) — a rule that had to be learned the hard way belongs here, so it
  is learned once.

**No secrets in any of them.** Describe a security-relevant change by its effect, never by
repeating the value involved. These repositories are public.
