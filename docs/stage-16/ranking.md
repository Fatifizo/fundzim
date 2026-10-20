# Stage 16 — Ranking

| Surface | Order | Inputs | Explainable as |
|---|---|---|---|
| Search, `sort=relevance` | `ts_rank_cd` with weights title 1.0, summary 0.4, category/place 0.2, organiser 0.1; ties newest, then id | the query and public text only | "Best match" |
| Typo fallback | trigram word similarity of the title; ties newest | the query and the title | "No exact matches; similar titles" |
| `sort=newest` / `oldest` | publication time, then id | — | as named |
| `sort=trending`, `/discovery/trending` | trending score ([trending.md](trending.md)); ties newest | unique visitors, unique sharers, age | the published rule |
| Featured | approved placement position, then window start | staff choice under maker-checker | "Featured by FundZim" |
| Featured fill | publication time | — | "Recently published" |
| Recommendations | rule order ([recommendations.md](recommendations.md)) | context campaign category / province, search terms, featured, trending, recency | the reason on each card |

Guarantees: deterministic for a given dataset (every order ends with a unique key); no paid placement, no hidden boost,
no money, no personal data; every promoted list is limited to promotable campaigns at serve time. Ranking weights that
are configurable are versioned and maker-checker ([trending.md](trending.md) §4); everything else is code, documented
here.
