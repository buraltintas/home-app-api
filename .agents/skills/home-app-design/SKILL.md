---
name: home-app-design
description: Shared visual, interaction, content, accessibility, and responsive design authority for the Boşa Gezme! consumer social-discovery product. Use whenever designing, implementing, reviewing, or polishing Boşa Gezme! web or mobile screens, components, tokens, navigation, localization, imagery, loading/empty/error states, or user flows.
---

# Boşa Gezme! design authority

Use this skill for both `ui` and `mobile`. Keep one conceptual design system; implement it with platform-native primitives. Read the backend handoff, OpenAPI, and canonical fixtures before designing data-backed states. Never invent DTO fields or backend capability.

## Authority and workflow

Use Impeccable first as the general craft, audit, and anti-pattern foundation, then apply this skill as the product-specific authority. If the two conflict, this skill wins. Impeccable's ambition means exceptional hierarchy, composition, typography, photography, interaction detail, and production quality here—not louder effects or more decoration.

Before changing a surface, inspect the existing implementation and identify the smallest coherent correction. Do not replace an established screen merely because a different composition is possible. After implementation, run the platform checks and inspect the actual UI at representative sizes; a passing build is not visual verification.

## Product thesis

Help people decide which real home/living store is worth visiting. Make the loop obvious: discover, visit, review, help someone else discover. Treat Boşa Gezme! as a visual consumer social product—not ecommerce, a marketplace, a merchant dashboard, or a generic review directory.

Prioritize three experiences above all others: home feed, natural-language search, and store detail. A user should understand within three seconds that real people share real visits to physical home stores.

Design mobile-first. Keep search a hero experience while hiding AI behind the scenes: use a familiar search interaction, never a chatbot, assistant persona, prompt theater, or “AI magic” treatment.

## Visual character

Create a warm, editorial, sophisticated, approachable, distinctive, and confident consumer-product composition where photography and authored content dominate. Express “light entering a home and making things clearer” through openness, clarity, contrast, and natural hierarchy—not literal rays or sun motifs.

Avoid generic AI/SaaS styling. Do not use blue/purple AI gradients, random decorative gradients, glassmorphism everywhere, glowing elements, meaningless abstract blobs, generic startup landing-page visuals, generic dashboard components, excessive rounded cards, card-inside-card layouts, pill overload, unnecessary borders, oversized empty whitespace without purpose, excessive centered layouts, or repeated icon + title + description cards. Avoid a uniform component rhythm that makes every screen feel templated. A floating navigation rail may use one restrained platform-native material blur when it improves spatial separation; do not repeat that glass treatment across content. Prefer purposeful space, strong type, large imagery, fine separators, and restrained surfaces. Let typography, spacing, composition, photography, store content, and hierarchy do most of the visual work.

Use the approved `Boşa Gezme!` product name and the role-specific supplied assets documented in `docs/brand/README.md`. Preserve the Turkish diacritics and exclamation mark in display copy. Use `brand-logo-transparent.png` on product surfaces, `app-icon-mascot.png` for small platform icons, the light/dark splash pair for native launch screens, and `social-share-banner.png` for wide social previews. Do not redraw, recolor, stretch, or crop essential lettering or mascot details. Use the text name when the full illustrated mark would be illegible at small sizes.

## Shared tokens

Name tokens semantically and map them to native/web implementations.

- Color: canvas `#F7F5F0`, surface `#FFFEFB`, surface-muted `#EFECE5`, ink `#262521`, ink-muted `#706D65`, line `#D9D5CC`, accent `#A34A32`, accent-strong `#833522`, success `#3F6B55`, warning `#9A672C`, error `#A63D35`. Preserve WCAG AA contrast; do not make the product entirely beige.
- Typography: use Source Sans 3 or another verified Latin Extended + Cyrillic sans for UI/body. Use Source Serif 4 only for selective editorial headings if the implementation can load both efficiently. Provide system fallbacks. Never select a font without confirming Turkish, German, English, and Russian coverage.
- Type scale: 12/16 meta, 14/20 secondary, 16/24 body, 20/26 title, 28/34 section display, 40/46 desktop editorial display. Prefer weight and spacing over oversized text.
- Spacing: use a 4-point base: 4, 8, 12, 16, 20, 24, 32, 40, 56, 72.
- Radius: 6 controls, 10 compact media, 16 feature media/sheets, 999 only for avatars, tags, and true capsules. Do not round every container.
- Border: one-pixel neutral line; use whitespace first, borders second.
- Elevation: none by default; one subtle shadow only for floating navigation, menus, dialogs, or bottom sheets.
- Icons: use one consistent outline icon family at 1.75–2 px. Keep common actions recognizable. Do not put every icon in a colored circle.
- Motion: 140–220 ms for feedback/transitions; use ease-out, respect reduced motion, avoid decorative looping animation.
- Touch/focus: minimum 44×44 pt mobile targets; provide a clearly visible accent focus ring on web.

## Photography and ratios

Give store, interior, product, and visit photography strong visual presence. Use editorial crops rather than decorative image tiles.

- Feed primary media: portrait 4:5 when one image is present.
- Store hero: 4:3 mobile and 16:9 or asymmetric editorial grid on desktop.
- Search/store rows: 4:3 thumbnail.
- Profile/photo grid: square.

Always set an explicit aspect ratio, meaningful alt text/accessibility labels, cover behavior, and a calm fallback surface. Never fabricate a backend cover image; fixture/development imagery must stay in a clearly separated presentation adapter.

## Mascot

Use the hunting-dog mascot sparingly for search empty states, discovery/loading states, no-results moments, onboarding, or another small branded beat. Keep it subordinate to store photography and community content. Do not repeat it across ordinary surfaces, surround it with cartoon decoration, or make the product feel childish. If animated, use one subtle entrance or micro-response; never add repetitive bouncing or noisy loops.

## Core compositions

### Home feed

Lead with people and places. Compose each post with author, store/location, subtle verified-visit signal, large media, rating, authored review, and social actions. Separate posts through spacing and thin rules rather than giant bordered cards. Keep store saving distinct from post liking. Prompt anonymous users to authenticate only after a protected action.

### Search

Make natural-language search unmistakable without presenting a chatbot. Use a prominent, conventional search field, examples, recent searches, a small category set, and contextual nearby discovery. Render interpreted intent as removable/editable chips only when useful.

On results, show store identity, category, address/distance, Boşa Gezme! community data, favorites, and optional Google data. Label the two sources independently. For Google-only stores, explicitly say there are no community reviews and invite the first visit review; never fabricate or blend scores.

### Store detail

Answer “Should I actually go there?” with strong imagery, store identity, category, neighborhood/distance, community rating and review count, favorites, then Save, Directions, Review, and Share. Follow with photos, community reviews, useful backend-supported details, and separately attributed external information. Do not create a product catalogue or invent phone, hours, website, or cover data.

## Trust, location, and auth

Render verified visit as a subtle success-neutral inline badge: check/footstep icon plus localized “Verified visit.” It communicates physical proximity, not identity verification or financial security.

Do not ask for location at launch. Explain the benefit immediately before requesting it for nearby search, maps, or review verification.

Keep browsing anonymous. When a signed-out user favorites, likes, follows, comments, or starts a review, open contextual authentication and preserve their intent. Offer Google and passwordless email OTP only; never add passwords.

## Transport architecture

Treat the server-side BFF boundary as non-negotiable for web. Browser code must never call the real backend origin directly. Browser components call same-origin Next.js BFF routes; only server components, route handlers, and other trusted server-side code may call the backend. The BFF owns backend origin selection, server credentials, HTTP-only auth cookies, visitor identity, locale forwarding, attribution headers, rate-limit metadata, and error normalization.

Mobile is the explicit exception because it cannot rely on the web BFF deployment. The native app calls the backend directly through its isolated typed transport. Assume every value bundled into the mobile client is extractable; a mobile client credential is abuse friction, not a secret or an authorization boundary.

## Navigation

- Mobile primary tabs: Home, Search, Create, Favorites, Profile. Present them on Boşa Gezme!'s floating platform-material navigation rail with fixed-width destinations, one restrained raised accent Create orb, and a softly springing compact translucent lens that travels between destinations but fades behind Create. The rail uses real native blur, a translucent warm tint, a quiet perimeter edge, and one soft shadow so it feels integrated rather than like a white card. Avoid drawn gloss stripes or highlight lines across the rail and Create orb; material depth should come from blur, opacity, and elevation. Mirror that material with `backdrop-filter` on responsive web. Use recognizable label-free icons with mandatory screen-reader labels, stable placement, and distinct restrained colors for destination identity. The Create orb is one uninterrupted accent surface without a contrasting border. Let the active icon lift and scale subtly; add press feedback and respect reduced motion. Pair a tab change with a 180–220 ms direction-aware content cross-fade, no more than 18 px of horizontal travel, and at most a 1–2% scale change; disable the entire scene transition when Reduce Motion is enabled. Use one professional outline icon family with consistent optical size and stroke. Preserve platform semantics, safe areas, and 44-point targets; do not use Unicode glyphs or a stock tab bar.
- Web primary navigation: Home, Discover, Favorites, Profile, Create Review. Use a restrained header/sidebar hybrid only when it improves desktop composition; do not mimic X/Twitter.

Use platform-native sharing, modals/sheets, back behavior, keyboard handling, and safe areas.

## Localization and content

Never hard-code visible strings in components. Define complete translation keys for `tr`, `en`, `de`, and `ru`; forward the same locale according to the backend contract. Format dates, numbers, distances, ratings, and plurals with locale-aware APIs. Keep user-authored reviews, comments, and bios untranslated. Keep canonical IDs/enums untranslated; display backend `category_labels` where supplied.

Write repository documentation, READMEs, contributor instructions, developer-facing explanations, and code comments in English only. This rule does not restrict localized product UI copy or locale-specific test fixtures.

Test long German labels, Cyrillic, Turkish dotted/dotless I and diacritics, empty plurals, and narrow screens. Use direct, warm, useful copy. Avoid hype, AI language, and merchant language.

Build flexible text containers and layouts from the start. Never approve a component because it works only with short English copy; validate all four locales before treating the component as complete.

## State and accessibility requirements

Design loading, empty, error, anonymous, authenticated, selected, disabled, pressed, and focused states. Prefer content-shaped skeletons for feed, search, and store detail. Explicitly cover community store, Google-only store, mixed search, zero results, degraded provider, and anonymous protected-action attempts.

Use semantic HTML, logical heading order, keyboard navigation, accessible dialogs, visible focus, descriptive labels, and non-color state cues on web. On mobile, label controls and state, respect dynamic text, screen readers, safe areas, and minimum touch sizes. Target WCAG AA where practical.

## Responsive behavior

Design mobile and desktop intentionally; do not stretch one layout. Validate web at 375, 390, 430, 768, 1024, 1280, and 1440 px. Use editorial desktop width for large imagery and optional results/map composition only when geography materially helps. Check clipping, crops, empty space, navigation, localization, and target sizes.

## Anti-slop implementation gate

Before accepting any screen, answer every question. Refine the design when an answer is weak.

1. Is the visual hierarchy obvious within three seconds?
2. Is there one clear primary action?
3. Are any containers, cards, pills, borders, or decorative effects unnecessary?
4. Does this resemble a generic AI-generated SaaS or startup template?
5. Could typography, spacing, composition, or photography replace decorative UI?
6. Is real store and community content carrying the interface?
7. Are hover, focus, pressed, selected, disabled, loading, empty, error, anonymous, and authenticated states intentional where relevant?
8. Does mobile usage feel natural and platform-appropriate?
9. Are touch targets at least 44×44 and keyboard/screen-reader behavior sound?
10. Does the screen work without clipping or awkward wrapping in `tr`, `en`, `de`, and `ru`?
11. Are community and Google data separate, and is every field backend-supported?
12. Does this feel specifically like Boşa Gezme! rather than any random app?

Inspect the running UI at target sizes and iterate on visible issues in bounded passes. A passing build alone does not satisfy this gate. Do not add animation for animation's sake; motion must explain state, continuity, or feedback and must respect reduced motion.
