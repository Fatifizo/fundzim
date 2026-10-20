# Stage 16 — Zimbabwe location discovery

## 1. What a location is

`campaigns.location_code` (optional): **where the cause is**, chosen by the organiser from a reference list — a province,
or a town that implies its province. It is reviewed content: part of the snapshot the reviewer approves, a change on a
live campaign is a material edit (`LOCATION`) and the public page keeps the approved location until re-review.

| Kind of location | Collected? | Shown? |
|---|---|---|
| Campaign (cause) location | yes, coarse, optional | yes: "Where: Mutare, Manicaland" / "Harare province" |
| Organiser's location | no | no |
| Beneficiary's location or address | no | no |
| Visitor's location | **no** (no geolocation, no IP lookup, no "near me") | — |

Never an address, a ward, a village or coordinates. "Not specified" is the default and is counted separately
(`unspecified_campaigns`); campaigns without a location appear in every unfiltered listing.

## 2. Reference list

`app.campaign_locations` (owned by campaigns; read-only for runtime roles; changed only by migration; every row carries
its source). Seeded with the ten provinces of Zimbabwe and their capitals as towns, from
https://en.wikipedia.org/wiki/Provinces_of_Zimbabwe (accessed 2026-10-19): Bulawayo (Bulawayo), Harare (Harare),
Manicaland (Mutare), Mashonaland Central (Bindura), Mashonaland East (Marondera), Mashonaland West (Chinhoyi), Masvingo
(Masvingo), Matabeleland North (Lupane), Matabeleland South (Gwanda), Midlands (Gweru). Codes are FundZim's own
(`PROVINCE_*`, `TOWN_*`), not ISO 3166-2 codes (not verified in this stage). More towns, districts or a country field for
causes outside Zimbabwe need a migration and the review in LR-090 (KI-S16-06).

## 3. Discovery

- Filters `province=<slug>` and `town=<slug>` on `/discovery/campaigns` (unknown values match nothing, never an error).
- `GET /discovery/locations`: provinces and towns with counts.
- `/campaigns/location/[province]` (indexable landing page per province; towns as chips; `?town=` filter), linked from
  the directory, campaign pages and the sitemap (provinces with at least one discoverable campaign).
- Search matches town and province names (weight C).
- Recommendations: "In the same province".

## 4. Privacy

A town combined with a sensitive category and a story can identify people in small communities. Mitigations: only
provincial capitals as towns, organiser choice, reviewer approval, province-only always possible. Whether some
categories or minors must be province-only is open: **LR-090, LEGAL_REVIEW_REQUIRED**.
