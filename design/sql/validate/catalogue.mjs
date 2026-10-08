// Cross-checks the SQL drafts against the table catalogue in docs/stage-2/design-baseline.md §5.
// - Every base table created by design/sql/NNNN_*.sql must be named in the baseline catalogue.
// - Every catalogue table must exist in the drafts.
// Usage: node catalogue.mjs   (exit 1 on any mismatch)
import { PGlite } from '@electric-sql/pglite';
import { readFileSync, readdirSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const sqlDir = join(here, '..');
const baseline = readFileSync(join(here, '../../../docs/stage-2/design-baseline.md'), 'utf8');
const cat = baseline.slice(baseline.indexOf('## 5. Table catalogue'), baseline.indexOf('## 6. '));

// Catalogue entries: backticked snake_case names in table rows / list lines of §5, excluding obvious
// non-table tokens (columns, values) listed below.
const NOT_TABLES = new Set(['account_kind', 'payment_id', 'risk', 'kyc', 'compliance', 'app', 'audit', 'psp',
  'payments', 'payouts', 'ledger', 'campaigns', 'beneficiaries', 'organisations', 'users', 'auth', 'storage',
  'fees', 'notifications', 'reconciliation', 'platform', 'legal_hold', 'confidential', 'payout_ids',
  'blocks_payouts', 'fundzim_app', 'kyc_gate_policy', 'requires_approval']);
const MODULE_AND_TABLE = new Set(['users', 'campaigns', 'beneficiaries', 'organisations']);
const expected = new Set();
for (const m of cat.matchAll(/`([a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)?)`/g)) {
  let n = m[1];
  if (n.includes('.')) { const [s, t] = n.split('.'); if (t === '*') continue; n = t; }
  if (n.startsWith('v_')) continue; // views are checked separately below
  if (MODULE_AND_TABLE.has(n)) { expected.add(n); continue; }
  if (NOT_TABLES.has(n) || !n.includes('_') && !['donations', 'chargebacks', 'users', 'sessions', 'roles', 'permissions', 'consents', 'identities', 'beneficiaries', 'organisations', 'campaigns', 'markets', 'currencies', 'holds', 'limits'].includes(n)) continue;
  expected.add(n);
}
// Brief-name aliases that are documented as mapped (not real tables).
for (const alias of ['payment_idempotency_keys', 'payment_webhook_inbox', 'transaction_limits', 'ledger_journals',
  'ledger_reconciliation_references', 'org_persons', 'org_check_results', 'org_representative_authorities',
  'verification_attempts', 'check_results', 'dispute_cases', 'eligibility_decisions', 'refunds', 'reviews',
  'kyc.organisations']) expected.delete(alias);

const db = new PGlite();
for (const f of readdirSync(sqlDir).filter((f) => /^\d{4}_.*\.sql$/.test(f)).sort()) {
  await db.exec(readFileSync(join(sqlDir, f), 'utf8'));
}
const { rows } = await db.query(`select table_schema s, table_name t from information_schema.tables
  where table_type='BASE TABLE' and table_schema in ('app','ledger','audit','kyc','risk','compliance','recon')
  order by 1,2`);
const actual = new Set(rows.map((r) => r.t));
const extra = rows.filter((r) => !expected.has(r.t) && r.t !== 'status_transitions' && !r.t.endsWith('_status_transitions'));
const missing = [...expected].filter((t) => !actual.has(t)).sort();
const views = (await db.query(`select table_schema||'.'||table_name v from information_schema.views
  where table_schema='compliance' order by 1`)).rows.map((r) => r.v);
for (const v of ['compliance.v_payout_blocking_cases', 'compliance.v_screening_status', 'compliance.v_active_restrictions'])
  if (!views.includes(v)) missing.push(v);
console.log(`tables in drafts: ${rows.length}; catalogue names: ${expected.size}`);
for (const s of [...new Set(rows.map((r) => r.s))]) console.log(`  ${s}: ${rows.filter((r) => r.s === s).length}`);
if (extra.length) console.log('NOT IN CATALOGUE: ' + extra.map((r) => `${r.s}.${r.t}`).join(', '));
if (missing.length) console.log('CATALOGUE TABLES MISSING FROM DRAFTS: ' + missing.join(', '));
console.log(extra.length || missing.length ? 'CATALOGUE CHECK: MISMATCH' : 'CATALOGUE CHECK: OK');
process.exit(extra.length || missing.length ? 1 : 0);
