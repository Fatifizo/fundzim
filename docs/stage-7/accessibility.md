# Stage 7 — Accessibility

Target: WCAG 2.2 level AA for the public experience and the new staff pages. **This document is not a conformance
claim.** Automated checks find only part of the WCAG failures, no manual screen-reader or assistive-technology
audit was done in Stage 7, and several criteria below are unverified.

## 1. What was built in

- `<html lang="en-ZW">`, a "Skip to main content" link, landmarks (`header`, `nav` with labels, `main`, `footer`), one
  `h1` per page, sections labelled by their headings (`aria-labelledby`).
- Visible focus for every interactive element (`:focus-visible` styles, including on dark backgrounds);
  `prefers-reduced-motion` honoured in `globals.css`.
- Touch targets at least 44 px high (`min-h-11`) for navigation, share and report controls.
- Forms: visible labels bound with `htmlFor`; errors linked with `aria-describedby` and `aria-invalid`, prefixed
  "Error:" for screen readers; the report reasons are a `fieldset` of radio buttons.
- Report dialog on the native `<dialog>` with `showModal()`: focus trap, inert background and Escape come from the
  browser; focus moves to the first control on open and returns to the trigger on close.
- Status messages (copy link, report confirmation) in `role="status"` / `aria-live="polite"` regions.
- External share links open in a new tab and say so; images have alt text (owner-supplied alt for campaign images,
  required by the Stage 6 media rules).
- Trust indicators are text, not icon-only; the disabled donate control has visible explanatory text.
- Layout reflows to 320 px without horizontal scrolling.

## 2. Automated checks (what they cover)

| Check | Scope |
|---|---|
| axe-core in Playwright (`withTags(["wcag2a","wcag2aa","wcag21a","wcag21aa","wcag22aa"])`, serious and critical violations fail) | Mock suite `e2e/public.spec.ts`: every public page plus a seeded campaign page (desktop and mobile projects). Real-stack `e2e-real/a11y.spec.ts`: home, discovery, a category, a campaign, safety, and the staff reports and featured pages signed in as real staff |
| axe-core in Vitest (`src/test/axe.ts`; `color-contrast` disabled because jsdom has no layout) | Component tests incl. share controls, report dialog and loading/error/empty states |
| Overflow at 320 px and 375 px, 44 px menu link height | `e2e/public.spec.ts` |
| Mobile menu: opens, closes with Escape, restores focus | `e2e/site.spec.ts` |
| Report dialog: validation messages, hidden honeypot | `e2e/public.spec.ts`, `report-dialog.test.tsx` |

Results of the final runs (2026-10-12): the real-stack axe checks passed on desktop and mobile in two consecutive runs; the mock-suite axe checks and the Vitest axe checks passed ([testing.md](testing.md) §0). Automated checks find only part of WCAG failures; see §3.

## 3. WCAG 2.2 AA criteria NOT verified in Stage 7

Each of these needs manual testing (keyboard-only walk-through, screen readers such as NVDA/JAWS on Windows,
VoiceOver on iOS/macOS and TalkBack on Android, zoom and text-spacing checks). Automated axe passes are not
evidence for them. Tracked as KI-S7-04.

| Criterion | Why unverified |
|---|---|
| 1.1.1 Non-text Content | Alt text exists, but its quality (owner-written alt for campaign photos) is not reviewed |
| 1.3.1 Info and Relationships | Only partly machine-checkable; no screen-reader reading-order review |
| 1.3.2 Meaningful Sequence | No screen-reader review |
| 1.3.4 Orientation | Not tested in landscape on devices |
| 1.3.5 Identify Input Purpose | The report email field has `autocomplete="email"`; other fields not audited |
| 1.4.3 Contrast (Minimum) | axe checks rendered text in Chromium only; text over cover images, hover/focus states and disabled controls not reviewed |
| 1.4.4 Resize Text (200%) | Not tested |
| 1.4.10 Reflow | Tested only as "no horizontal overflow" at 320/375 px, not at 400% zoom on desktop |
| 1.4.11 Non-text Contrast | Focus rings, input borders and icons not measured |
| 1.4.12 Text Spacing | Not tested |
| 1.4.13 Content on Hover or Focus | Not reviewed |
| 2.1.1 Keyboard / 2.1.2 No Keyboard Trap | Only the mobile menu and report dialog are exercised; no full keyboard-only walk-through |
| 2.4.3 Focus Order | Not reviewed beyond the dialog and menu |
| 2.4.6 Headings and Labels | Present, but their descriptiveness is not reviewed |
| 2.4.7 Focus Visible | Styles exist; not checked on every component |
| 2.4.11 Focus Not Obscured (Minimum) (new in 2.2) | The site header is `sticky`; whether it hides focused elements when tabbing backwards is not tested |
| 2.5.3 Label in Name | Not audited |
| 2.5.7 Dragging Movements (new in 2.2) | No dragging is expected on public pages; not audited |
| 2.5.8 Target Size (Minimum) (new in 2.2) | 44 px is applied to the main controls; inline links and pagination not measured |
| 3.2.6 Consistent Help (new in 2.2) | Help/contact placement not audited |
| 3.3.1 / 3.3.3 Error Identification and Suggestion | Report dialog only; staff pages not reviewed |
| 3.3.7 Redundant Entry / 3.3.8 Accessible Authentication (new in 2.2) | Not in Stage 7 scope for public pages; sign-in (TOTP, password) not re-audited |
| 4.1.2 Name, Role, Value | Machine-checked only |
| 4.1.3 Status Messages | Live regions exist; not verified with screen readers |

Also not verified: behaviour on low-end Android devices and Opera Mini/KaiOS, and Shona or Ndebele content (English
only; `lang` is not set per campaign — KI-S7-08).

## 4. Next steps

A manual accessibility audit of the core flows (home → discover → campaign → share → report; staff report triage
and featured management) with at least one desktop and one mobile screen reader, recorded with dates and tools, before
any public launch.
