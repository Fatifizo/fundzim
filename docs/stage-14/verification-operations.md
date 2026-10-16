# Stage 14 — Verification operations

Page `/admin/operations/verification` (`kyc.case.review`). Sources: `kyc.OpsSnapshot` (KYC/KYB cases, on the
`fundzim_kyc` pool), `beneficiaries.OpsSnapshot`, `compliance.OpsSnapshot` (on the `fundzim_compliance` pool).

- Metrics: KYC and KYB reviews pending and awaiting the applicant, escalated verification cases, beneficiary
  verifications pending, open compliance cases, restricting resolutions awaiting approval, subjects under a restriction.
- Approvals: KYC/KYB second decisions, beneficiary second approvals, compliance restricting resolutions.
- No identity document, number, image or compliance note appears in the portal; case detail stays in the verification
  and compliance modules with their audited access. Compliance cases of type `RESTRICTED_STR` are excluded from every
  listing (counted nowhere outside compliance).
- Search: `kyc.OpsSearch` by exact KYC or KYB case id (`kyc.case.review`).
