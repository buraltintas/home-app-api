# Product

<!-- impeccable:product-schema 1 -->

## Platform

adaptive

## Users

People deciding whether a physical home or living store is worth visiting. They may browse anonymously while planning a trip, compare nearby options, or contribute an account-backed review after a real visit.

## Product Purpose

Boşa Gezme! helps people discover real physical home and living stores, visit with better expectations, review what they experienced, and help the next person discover. Success means a user can answer “Which store should I actually visit?” from authentic store photography, community visits, and clearly attributed supporting information.

## Positioning

The product is a consumer social-discovery experience centered on physical visits. It is not ecommerce, a merchant directory, a SaaS dashboard, or a chatbot. Search may use AI in the backend, but the user interacts with a conventional discovery experience rather than an AI persona.

## Operating Context

The primary loop is **discover → visit → review → help someone else discover**. The core surfaces are Home Feed, Search and Results, and Store Detail. Web runs as a Next.js experience through a server-side BFF; native iOS and Android clients use React Native and call the backend through an isolated typed transport. Anonymous browsing is a first-class state, while social mutations and reviews require authentication.

## Capabilities and Constraints

- Support Turkish, English, German, and Russian, including variable text lengths and Cyrillic.
- Keep Boşa Gezme! community data separate from externally sourced Google data.
- Use only backend-supported fields and capabilities; never fabricate store facts, scores, reviews, or media.
- Request location only in context and never treat a manually selected discovery location as visit proof.
- Preserve the web BFF boundary. Mobile is the explicit direct-transport exception.
- Respect platform conventions instead of forcing pixel-identical web and native implementations.

## Brand Commitments

The approved display name is **Boşa Gezme!** and the canonical domain is `bosagezme.com`. Preserve Turkish diacritics and the exclamation mark. The approved hunting-dog brand family is documented in `docs/brand/README.md`; use each supplied asset only for its intended surface and never redraw, recolor, distort, or let it overpower real store content. Product copy should be direct, warm, confident, and free of AI hype.

## Evidence on Hand

- Canonical backend and client contract: `docs/frontend-handoff.md` and `docs/openapi.yaml`.
- Valid API fixtures: `docs/frontend-fixtures/`.
- Approved asset inventory and platform routing: `docs/brand/README.md`.
- Existing web implementation: sibling `ui/` repository.
- Existing native implementation: sibling `mobile/` repository.
- No testimonials, merchant claims, or performance claims are approved; future work must not invent them.

## Product Principles

1. Let real stores, real visits, and authored community content carry the experience.
2. Keep discovery useful without requiring an account or exposing the AI behind search.
3. Make trust and source attribution understandable without adding heavy interface chrome.
4. Design mobile-first while adapting deliberately to web, iOS, and Android conventions.
5. Make every contribution help another person decide whether a physical visit is worthwhile.

## Accessibility & Inclusion

Target WCAG AA on web and platform-appropriate accessibility on mobile. Support keyboard and screen-reader use, visible focus, reduced motion, dynamic text where practical, safe areas, and minimum 44×44 touch targets. Validate Turkish casing and diacritics, long German copy, and Cyrillic layouts from the start.
