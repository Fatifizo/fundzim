/**
 * Staff navigation (Stage 14 admin shell). Each entry names the permission its page's API route enforces, so a staff
 * member is shown only what they can open. This is presentation, never authorization: every API route enforces its
 * own policy and a page without the permission renders the 404 page (requireStaff + serverGetOr404).
 */

export interface NavItem {
  href: string;
  label: string;
  /** Permission the page's API route requires; null = any staff member. */
  permission: string | null;
  exact?: boolean;
}

export interface NavGroup {
  id: string;
  label: string;
  items: NavItem[];
}

const DASH = "ops.dashboard.view";

export const NAV_GROUPS: NavGroup[] = [
  {
    id: "operations",
    label: "Operations",
    items: [
      { href: "/admin/operations", label: "Command center", permission: DASH, exact: true },
      { href: "/admin/approvals", label: "Approvals", permission: DASH },
      { href: "/admin/operations/exceptions", label: "Exceptions", permission: DASH },
      { href: "/admin/operations/health", label: "Platform health", permission: DASH },
      { href: "/admin/operations/incidents", label: "Incidents", permission: "ops.incident.view" },
      { href: "/admin/operations/sla", label: "Service targets", permission: DASH },
      { href: "/admin/reports", label: "Reports", permission: DASH },
      { href: "/admin/search", label: "Search", permission: DASH },
    ],
  },
  {
    id: "module-ops",
    label: "Module operations",
    items: [
      { href: "/admin/operations/campaigns", label: "Campaign operations", permission: "campaign.view" },
      { href: "/admin/operations/verification", label: "Verification operations", permission: "kyc.case.review" },
      { href: "/admin/operations/payments", label: "Payment operations", permission: "payment.view" },
      { href: "/admin/operations/ledger", label: "Ledger operations", permission: "ledger.view" },
      { href: "/admin/operations/payouts", label: "Payout operations", permission: "payout.view" },
    ],
  },
  {
    id: "support",
    label: "Support",
    items: [{ href: "/admin/support", label: "Support cases", permission: "ops.support.view" }],
  },
  {
    id: "communications",
    label: "Communications",
    items: [
      { href: "/admin/communications", label: "Communications", permission: "communications.view", exact: true },
      { href: "/admin/communications/deliveries", label: "Deliveries", permission: "communications.view" },
      { href: "/admin/communications/dead-letters", label: "Dead letters", permission: "communications.view" },
      { href: "/admin/communications/templates", label: "Templates", permission: "communications.view" },
      { href: "/admin/communications/providers", label: "Messaging providers", permission: "communications.view" },
      { href: "/admin/communications/suppressions", label: "Suppressions", permission: "communications.view" },
      { href: "/admin/communications/reports", label: "Communication reports", permission: "communications.view" },
      { href: "/admin/communications/consent", label: "Consent records", permission: "communications.consent.view" },
    ],
  },
  {
    id: "staff",
    label: "Staff and audit",
    items: [
      { href: "/admin/staff", label: "Staff directory", permission: "staff.view", exact: true },
      { href: "/admin/staff/roles", label: "Roles and permissions", permission: "staff.view" },
      { href: "/admin/staff/access-reviews", label: "Access reviews", permission: "staff.view" },
      { href: "/admin/audit", label: "Audit log", permission: "audit.read" },
    ],
  },
  {
    id: "verification",
    label: "Verification",
    items: [
      { href: "/admin/verification", label: "All verification", permission: "kyc.case.review", exact: true },
      { href: "/admin/verification/kyc", label: "Identity (KYC)", permission: "kyc.case.review" },
      { href: "/admin/verification/kyb", label: "Organisations (KYB)", permission: "kyc.case.review" },
      { href: "/admin/verification/beneficiaries", label: "Beneficiaries", permission: "kyc.case.review" },
      { href: "/admin/verification/payout-destinations", label: "Payout accounts", permission: "kyc.case.review" },
      { href: "/admin/compliance/cases", label: "Compliance cases", permission: "compliance.case.view" },
    ],
  },
  {
    id: "campaigns",
    label: "Campaigns",
    items: [
      { href: "/admin/campaigns/review", label: "Campaign reviews", permission: "campaign.review" },
      { href: "/admin/campaigns", label: "All campaigns", permission: "campaign.view", exact: true },
      { href: "/admin/campaigns/updates", label: "Update moderation", permission: "content.moderate" },
      { href: "/admin/campaigns/reports", label: "Campaign reports", permission: "campaign.review" },
    ],
  },
  {
    id: "discovery",
    label: "Discovery",
    items: [
      { href: "/admin/discovery", label: "Discovery overview", permission: "discovery.view", exact: true },
      { href: "/admin/discovery/featured", label: "Featured placements", permission: "discovery.view" },
      { href: "/admin/discovery/ranking", label: "Trending ranking", permission: "discovery.view" },
    ],
  },
  {
    id: "payments",
    label: "Payments (TEST)",
    items: [
      { href: "/admin/payments", label: "Payments overview", permission: "payment.view", exact: true },
      { href: "/admin/payments/attempts", label: "Payment attempts", permission: "payment.view" },
      { href: "/admin/payments/unknown", label: "Unknown payments", permission: "payment.view" },
      { href: "/admin/payments/intents", label: "Donation intents", permission: "payment.view" },
      { href: "/admin/payments/webhooks", label: "Payment webhooks", permission: "payment.view" },
      { href: "/admin/payments/reconciliation", label: "Reconciliation", permission: "payment.view" },
      { href: "/admin/payments/refunds", label: "Refund requests", permission: "payment.view" },
      { href: "/admin/payments/providers", label: "Payment providers", permission: "payment.view" },
      { href: "/admin/payments/health", label: "Provider health", permission: "payment.view" },
    ],
  },
  {
    id: "ledger",
    label: "Ledger (TEST)",
    items: [
      { href: "/admin/finance/ledger", label: "Ledger", permission: "ledger.view", exact: true },
      { href: "/admin/finance/accounts", label: "Ledger accounts", permission: "ledger.view" },
      { href: "/admin/finance/journals", label: "Journals", permission: "ledger.view" },
      { href: "/admin/finance/trial-balance", label: "Trial balance", permission: "ledger.view" },
      { href: "/admin/finance/reconciliation", label: "Ledger reconciliation", permission: "ledger.view" },
      { href: "/admin/finance/exceptions", label: "Ledger exceptions", permission: "ledger.view" },
    ],
  },
  {
    id: "payouts",
    label: "Payouts (TEST)",
    items: [
      { href: "/admin/finance/payouts", label: "Payouts", permission: "payout.view", exact: true },
      { href: "/admin/finance/withdrawals", label: "Withdrawal requests", permission: "payout.view" },
      { href: "/admin/finance/payouts/approvals", label: "Payout approvals", permission: "payout.view" },
      { href: "/admin/finance/payouts/reservations", label: "Payout reservations", permission: "payout.view" },
      { href: "/admin/finance/payouts/exceptions", label: "Payout exceptions", permission: "payout.view" },
      { href: "/admin/finance/payouts/reconciliation", label: "Payout reconciliation", permission: "payout.view" },
    ],
  },
  {
    id: "fees",
    label: "Fees (TEST)",
    items: [
      { href: "/admin/finance/fees", label: "Fees", permission: "fee.view", exact: true },
      { href: "/admin/finance/fees/policies", label: "Fee policies", permission: "fee.view" },
      { href: "/admin/finance/fees/provider-schedules", label: "Provider fee evidence", permission: "fee.view" },
      { href: "/admin/finance/fees/quotes", label: "Fee quotes", permission: "fee.view" },
      { href: "/admin/finance/fees/assessments", label: "Fee assessments", permission: "fee.view" },
      { href: "/admin/finance/fees/adjustments", label: "Fee adjustments", permission: "fee.view" },
      { href: "/admin/finance/fees/reconciliation", label: "Fee reconciliation", permission: "fee.view" },
      { href: "/admin/finance/revenue", label: "Revenue (TEST)", permission: "fee.view" },
    ],
  },
  {
    id: "risk",
    label: "Risk",
    items: [
      { href: "/admin/risk", label: "Risk", permission: "risk.operations.view", exact: true },
      { href: "/admin/risk/signals", label: "Risk signals", permission: "risk.operations.view" },
      { href: "/admin/risk/evaluations", label: "Risk evaluations", permission: "risk.operations.view" },
      { href: "/admin/risk/rules", label: "Risk rules", permission: "risk.operations.view" },
      { href: "/admin/risk/cases", label: "Risk cases", permission: "risk.operations.view" },
      { href: "/admin/risk/holds", label: "Risk holds", permission: "risk.operations.view" },
      { href: "/admin/risk/screening", label: "Screening (TEST)", permission: "risk.operations.view" },
      { href: "/admin/risk/monitoring", label: "Risk monitoring", permission: "risk.operations.view" },
    ],
  },
  {
    id: "account",
    label: "My staff account",
    items: [
      { href: "/admin/notifications", label: "My notifications", permission: null },
      { href: "/admin/account/personal-link", label: "Personal account link", permission: null },
    ],
  },
];

/** A permission check on the caller's own effective permissions (from the session). Presentation only. */
export function can(permissions: readonly string[] | undefined, permission: string | null): boolean {
  if (permission === null) return true;
  return (permissions ?? []).includes(permission);
}

/** The groups and items this staff member can open (empty groups are dropped). */
export function visibleNav(permissions: readonly string[] | undefined): NavGroup[] {
  return NAV_GROUPS.map((g) => ({ ...g, items: g.items.filter((i) => can(permissions, i.permission)) })).filter((g) => g.items.length > 0);
}

const SEGMENT_LABELS: Record<string, string> = {
  admin: "Staff",
  operations: "Operations",
  approvals: "Approvals",
  exceptions: "Exceptions",
  health: "Platform health",
  incidents: "Incidents",
  sla: "Service targets",
  reports: "Reports",
  search: "Search",
  support: "Support cases",
  staff: "Staff",
  roles: "Roles and permissions",
  "access-reviews": "Access reviews",
  audit: "Audit log",
  verification: "Verification",
  campaigns: "Campaigns",
  payments: "Payments",
  ledger: "Ledger",
  payouts: "Payouts",
  finance: "Finance",
  fees: "Fees",
  risk: "Risk",
  compliance: "Compliance",
  cases: "Cases",
  account: "Account",
  communications: "Communications",
  deliveries: "Deliveries",
  "dead-letters": "Dead letters",
  templates: "Templates",
  providers: "Messaging providers",
  suppressions: "Suppressions",
  consent: "Consent records",
  notifications: "Notifications",
};

const UUIDISH = /^[0-9a-f]{8}-[0-9a-f]{4}-/i;

const PAGES = new Set(["/admin", ...NAV_GROUPS.flatMap((g) => g.items.map((i) => i.href))]);

/**
 * Breadcrumb trail for a staff path: known segments are labelled, record ids are shortened ("Record 0190ab12"). Only
 * crumbs that are pages get an href (`/admin/finance` is a path segment, not a page).
 */
export function breadcrumbs(pathname: string): Array<{ href: string | null; label: string }> {
  const parts = pathname.split("/").filter(Boolean);
  if (parts[0] !== "admin") return [];
  const out: Array<{ href: string | null; label: string }> = [];
  let href = "";
  for (const p of parts) {
    href += `/${p}`;
    let label = SEGMENT_LABELS[p];
    if (!label) label = UUIDISH.test(p) ? `Record ${p.slice(0, 8)}` : decodeURIComponent(p).replace(/[-_]/g, " ");
    out.push({ href: PAGES.has(href) ? href : null, label });
  }
  return out;
}
