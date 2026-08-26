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
