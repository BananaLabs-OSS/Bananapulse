// Seed a starter component tree matching the default pulse.config.ts scopes.
// Edit the rows to model YOUR org → products → services/hosts, then run:
//   node --env-file=.env scripts/seed-tree.mjs
// DESTRUCTIVE: truncates components/incidents/observations/sources first.
import postgres from 'postgres';
const sql = postgres(process.env.DATABASE_URL, { ssl: (process.env.DATABASE_URL||'').includes('localhost') ? false : 'require' });

const allOk = Array.from({ length: 90 }, () => 'ok');

// [id, parent, name, kind, tag(display), status, sort, brand, domain]
// The root id must match a scope id in src/pulse.config.ts.
const T = [
  ['example', null,      'Example Co', 'organization', 'company', 'ok', 0, null, 'status.example.com'],
  ['web',     'example', 'Website',    'service',      'edge',    'ok', 0, null, null],
  ['api',     'example', 'API',        'service',      'edge',    'ok', 1, null, null],
  ['email',   'example', 'Email',      'service',      'email',   'ok', 2, null, null],
];

await sql.begin(async (sql) => {
  await sql`TRUNCATE components, incidents, incident_timeline, observations, source_target_map, sources RESTART IDENTITY CASCADE`;
  for (const [id, parent, name, kind, tag, status, sort, brand, domain] of T) {
    await sql`INSERT INTO components (id, parent_id, name, kind, tag, status, uptime_90d, sort_order, brand, domain)
      VALUES (${id}, ${parent}, ${name}, ${kind}, ${tag}, ${status}, ${JSON.stringify(allOk)}::jsonb, ${sort}, ${brand}, ${domain})`;
  }
});

const n = await sql`SELECT count(*)::int c FROM components`;
console.log(`seeded ${n[0].c} components`);
await sql.end();
