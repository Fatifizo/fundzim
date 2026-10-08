// Validates the Stage 2 SQL DESIGN DRAFTS (design/sql/*.sql) in an in-memory PGlite instance.
// This never connects to a real database. Usage: (cd design/sql/validate && npm ci && npm run validate)
//
// 1. Loads every design/sql/NNNN_*.sql file in numeric order into a fresh in-memory database.
// 2. For each design/sql/tests/*.sql file, starts from a fresh load and runs its cases in order.
//    A case starts with a marker line:
//        -- @case <name> expect=ok
//        -- @case <name> expect=error:<substring of the expected error message>
//    Each case runs in its own transaction (BEGIN ... COMMIT), so deferred constraint triggers fire.
//    A case expected to succeed commits, and its effects are visible to later cases in the same file.
// Exit code 0 only if every file loads and every case behaves as expected.
import { PGlite } from '@electric-sql/pglite';
import { readFileSync, readdirSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const sqlDir = join(dirname(fileURLToPath(import.meta.url)), '..');
const testDir = join(sqlDir, 'tests');
const schemaFiles = readdirSync(sqlDir).filter((f) => /^\d{4}_.*\.sql$/.test(f)).sort();
const testFiles = readdirSync(testDir).filter((f) => f.endsWith('.sql')).sort();

let failures = 0;
const log = (s) => process.stdout.write(s + '\n');

async function freshDb() {
  const db = new PGlite();
  for (const f of schemaFiles) {
    try {
      await db.exec(readFileSync(join(sqlDir, f), 'utf8'));
    } catch (e) {
      throw new Error(`schema file ${f} failed to load: ${e.message}`);
    }
  }
  return db;
}

// Schema load check
try {
  const db = await freshDb();
  const t = await db.query(
    `select table_schema, count(*)::int n from information_schema.tables
     where table_type='BASE TABLE' and table_schema not in ('pg_catalog','information_schema')
     group by 1 order by 1`);
  log(`LOAD OK: ${schemaFiles.length} files`);
  for (const r of t.rows) log(`  schema ${r.table_schema}: ${r.n} tables`);
  await db.close();
} catch (e) {
  log(`LOAD FAIL: ${e.message}`);
  process.exit(1);
}

for (const tf of testFiles) {
  const text = readFileSync(join(testDir, tf), 'utf8');
  const parts = text.split(/^-- @case /m).slice(1);
  let db;
  try { db = await freshDb(); } catch (e) { log(`FAIL ${tf}: ${e.message}`); failures++; continue; }
  let pass = 0;
  for (const part of parts) {
    const nl = part.indexOf('\n');
    const header = part.slice(0, nl).trim();
    const body = part.slice(nl + 1);
    const m = header.match(/^(\S+)\s+expect=(ok|error(?::(.*))?)$/);
    if (!m) { log(`FAIL ${tf}: bad case header "${header}"`); failures++; continue; }
    const [, name, kind, pattern] = m;
    let err = null;
    try {
      await db.exec(`BEGIN;\n${body}\nCOMMIT;`);
    } catch (e) {
      err = e;
      try { await db.exec('ROLLBACK;'); } catch { /* not in a transaction */ }
    }
    const wantErr = kind.startsWith('error');
    let ok;
    if (!wantErr) ok = err === null;
    else ok = err !== null && (!pattern || err.message.includes(pattern.trim()));
    if (ok) pass++;
    else {
      failures++;
      log(`FAIL ${tf} :: ${name} — expected ${kind}, got ${err ? 'error: ' + err.message : 'success'}`);
    }
  }
  log(`${pass === parts.length ? 'PASS' : 'FAIL'} ${tf}: ${pass}/${parts.length} cases`);
  await db.close();
}

log(failures === 0 ? 'ALL CHECKS PASSED' : `${failures} FAILURE(S)`);
process.exit(failures === 0 ? 0 : 1);
