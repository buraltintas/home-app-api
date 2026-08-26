# Workspace design workflow

For frontend or product-design work in the sibling `ui` and `mobile` repositories, use `.agents/skills/impeccable/SKILL.md` as the general quality foundation and `.agents/skills/home-app-design/SKILL.md` as the Boşa Gezme!-specific authority. The product-specific authority wins when the two conflict. Do not apply UI-only guidance by changing backend application logic.

Keep repository documentation and developer-facing explanations in English.

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
