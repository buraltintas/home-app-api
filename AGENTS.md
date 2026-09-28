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

## A migration and the code that needs it cannot ship together

Deploys here do not run migrations. They are a separate release step, and nothing in the
pipeline will tell you that you forgot: the build is green, because the build compiles code
and never sees the database.

So a change that adds a column and reads it in the same commit goes live as a binary querying
a column that does not exist. Every request on that path fails. This cost the site every
store page for eight minutes, and the symptom -- a 500 from one endpoint -- looks nothing like
the cause.

Two deploys, in this order, whenever a change needs new schema:

1. The migration on its own, applied to the database.
2. The code that reads or writes it, once step 1 has actually run.

The same holds for dropping a column: the code stops using it first, then the column goes.

## A database that never sleeps is a traffic question before it is a code question

The managed Postgres suspends itself after five quiet minutes and bills the hours it is
awake, so anything that touches it on a short cycle costs a full day of compute. The obvious
suspects are ours -- pollers, tickers, sweeps -- and reading the code will always find some.
It found two here, and neither was the reason.

Count the gaps first. One query over a day of request logs, bucketed into five-minute
windows, answers it outright:

```bash
gcloud logging read 'resource.type="cloud_run_revision" AND resource.labels.service_name="<service>" AND httpRequest.requestMethod!="" AND timestamp>="<from>" AND timestamp<"<to>"' \
  --project <project> --limit 20000 --format='value(timestamp)'
```

If no window is empty, no timer can be to blame -- there was never a gap for it to sit in,
and changing its period saves nothing. The cause is upstream, in who is asking. If gaps do
exist, then the periods matter, and the test for each is whether it is longer than the gaps
it is sitting in: fifteen minutes against gaps of five to fifteen is the same as no gap at
all.

Fix the traffic, then size the timers for the quiet that fixing it creates.

## A limit is only as real as the thing it counts

Two of them here were counting nothing.

The cap on sign-in codes counted requests per IP address, and the API never sees a
visitor's address: every request arrives from the web server, so one address stood for the
whole product. Ten an hour, for everybody, emptiable by anyone. The rate limiter had the
matching fault from the other direction -- it counted against a header the caller writes,
so a fresh value per request bought an unlimited allowance on every limiter, search
included, where each request is a model call we pay for.

Both had been written carefully and neither had been checked against the data it produced.
That check is one query and it is the only thing that settles it:

```sql
SELECT encode(substring(request_ip_hash from 1 for 4),'hex'), count(*), count(DISTINCT normalized_email)
FROM email_verification_codes GROUP BY 1 ORDER BY 2 DESC;
```

One row came back. Twenty-one codes, six addresses, a month apart, one bucket.

So when a limit is added or changed: name what distinguishes one caller from the next, ask
whether the caller can choose it, and then go and look at the values the running system
actually stored. A limit keyed on something the caller controls is a formality; a limit
keyed on something every caller shares is an outage waiting for its first busy hour.

And when the key is a shared address rather than a person, size it for several strangers at
once -- a household, an office, and a mobile carrier's NAT are all one address.

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
