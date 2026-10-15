# Stage 12 — Rounding

| Item | Rule |
|---|---|
| Modes | `HALF_UP` (.5 away from zero — fees are non-negative, so up), `HALF_EVEN` (.5 to the even neighbour), `FLOOR`, `CEILING`. Each policy version and provider schedule names its mode; the platform default in examples is HALF_UP (MONEY.md §5). |
| Rounding stage | Once per component, on the exact rational value (percentage part plus any fixed part), before the minimum and maximum clamps. Intermediate values are never rounded and summed (MONEY.md §5). |
| Rounding unit | One minor unit of the currency (USD: 2 decimals). ZWG is not usable (LR-043), so no ZWG fee can be computed. |
| Currency precision | Amounts are integer minor units; the registry's minor units are authoritative. |
| Component ordering | Platform fee, then provider-cost fee; each is independent (neither is computed on the other). |
| Residual handling | SHARED splits use largest-remainder allocation (ties to the donor) so parts sum exactly; cumulative reversals (refunds, chargebacks) compute `round(refundable × cumulative_refunded / basis) − reversed_before`, so a series of partial refunds reverses exactly what one refund of the same total would — no drift, and a full refund of a PROPORTIONAL fee reverses all of it. |
| Provider rounding | UNKNOWN unless the evidence states it (`rounding_mode` NULL). Estimates then use HALF_UP and say so in the component note; FundZim never guesses provider rounding as fact. |
| Audit | Each component stores its unrounded value (`exact_value`, "numerator/denominator") and the mode used. |

Boundary tests (`TestRoundingModesAtBoundaries`): 16.65, 16.5, 17.5, 16.1, exact values and sub-unit values under every
mode; property tests check every rounded fee is within one minor unit of the floor of its exact value.
