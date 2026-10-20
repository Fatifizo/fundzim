# Stage 16 — Recommendations

Code: `internal/discovery/recommendations.go` (`Recommend`). Contract: `GET /api/v1/discovery/recommendations?campaign=&category=&q=&limit=`.

## 1. Rules (applied in order until `limit`, default 4, max 12; deduplicated; the viewed campaign excluded)

| # | Rule | Reason code / text | When |
|---|---|---|---|
| 1 | Matches the search terms (relevance order) | `RELATED_TO_SEARCH` "Related to your search" | a `q` is given |
| 2 | Same category as the viewed campaign (or the given category), trending score then newest | `MORE_IN_CATEGORY` "More in this category" | context campaign or category |
| 3 | Same province as the viewed campaign, newest | `SAME_PROVINCE` "In the same province" | context campaign has a location |
| 4 | Approved featured placements in their window | `FEATURED` "Featured by FundZim" | always |
| 5 | Trending (rank eligible) | `POPULAR_NOW` "Popular right now" | always |
| 6 | Recently published | `RECENTLY_PUBLISHED` "Recently published" | always |

Every candidate goes through `campaigns.PublicCards(promotable=true)`: only active campaigns without restrictions or
holds are recommended.

## 2. Explainability and privacy

- Each item carries its reason; the response carries the rule text ("Chosen by fixed rules, the same for everyone … Nothing
  about you is used."), shown on the campaign page under "More campaigns".
- `personalised` is always `false`. The web app refuses to render a response claiming otherwise.
- Inputs are only the request parameters and public campaign data. Never the viewer's account, history, address,
  location, cookies, KYC, screening results, risk scores or holds (holds only exclude), and no profile is built.
- No AI/ML model; a learned model would need a documented need, an ADR and a privacy review.

## 3. Placement

Campaign page "More campaigns" (3 cards, server-rendered after the main content, failures render nothing). The same
endpoint can serve search-empty states or category pages later.
