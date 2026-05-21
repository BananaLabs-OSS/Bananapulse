// integration.ts — the AstroIntegration factory.
//
// Hooks used:
//   astro:config:setup  → injectRoute() to mount /status and /status/incidents,
//                         plus a Vite virtual module that exposes consumer
//                         config to the injected route components.
//   astro:build:done    → write the Atom feed (incidents.xml) into dist/ at
//                         the configured mount path. Done at build time
//                         because the engine is pure-static — there is no
//                         server to render the feed on request.
//
// Pure static: no @astrojs/cloudflare, no SSR, no runtime env. All upstream
// polling happens client-side from the browser, in the <script> shipped by
// the injected route. CORS on the consumer's upstream is their problem to
// solve; see DEPLOY.md.

import type { AstroIntegration } from 'astro';
import { fileURLToPath } from 'node:url';
import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { readFileSync, existsSync } from 'node:fs';
import { assertIncident, type Incident } from './lib/canonical.js';

// ── public config types ─────────────────────────────────────────────────

export type BananapulseSourceType = 'queue-status';

export interface BananapulseSource {
  /** Absolute URL of the upstream JSON the browser will fetch. Must be
   *  CORS-reachable from the deployed status page's origin. */
  url: string;
  /** Mapper to apply to the upstream response. v0.1 ships `queue-status`;
   *  add more by extending the switch in src/lib/ingest.ts. */
  type: BananapulseSourceType;
}

/** Optional mapping of Bananapulse CSS custom-property names → consumer
 *  CSS custom-property names. Lets a consumer reuse their existing
 *  design tokens without forking the default theme. Example:
 *    { '--accent': '--brand-blue', '--bg': '--surface-0' }
 *  Anything not mapped falls through to the default-theme.css values. */
export type BananapulseThemeMap = Record<string, string>;

export interface BananapulseConfig {
  /** URL path to mount the status page on. The incidents history page
   *  is mounted at `${mountPath}/incidents`, and the Atom feed at
   *  `${mountPath}/incidents.xml`. Default: '/status'. */
  mountPath?: string;
  /** Human-readable site name shown in the page title + headings. */
  name: string;
  /** Domain string used for the Atom feed tag URI + display. */
  domain: string;
  /** Upstream status sources. v0.1 only renders the first one. */
  sources: BananapulseSource[];
  /** Path (relative to the consumer's project root) to a JSON file of
   *  incidents. Default: './src/data/incidents.json'. Read at build
   *  time only. */
  incidentsPath?: string;
  /** Optional CSS-var remap. See BananapulseThemeMap. */
  themeCssVarMap?: BananapulseThemeMap;
  /** Override the default-theme.css with a consumer-owned path
   *  (resolved from project root). If set, default-theme.css is NOT
   *  imported by the injected routes — yours is used instead. */
  themeCssPath?: string;
}

// ── internals ───────────────────────────────────────────────────────────

const DEFAULT_MOUNT = '/status';
const DEFAULT_INCIDENTS_PATH = './src/data/incidents.json';

/** Normalise a mountPath: must start with '/', must not end with '/'
 *  (unless it IS '/'). */
function normaliseMount(p: string | undefined): string {
  let m = (p ?? DEFAULT_MOUNT).trim();
  if (!m.startsWith('/')) m = '/' + m;
  if (m.length > 1 && m.endsWith('/')) m = m.slice(0, -1);
  return m;
}

function escapeXml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;');
}

function readIncidents(absPath: string): Incident[] {
  if (!existsSync(absPath)) return [];
  let parsed: unknown;
  try {
    parsed = JSON.parse(readFileSync(absPath, 'utf8'));
  } catch {
    return [];
  }
  if (!Array.isArray(parsed)) return [];
  const out: Incident[] = [];
  for (const i of parsed) {
    try {
      assertIncident(i);
      out.push(i);
    } catch {
      // skip silently; consumer will notice the missing row on the page
    }
  }
  return out.sort((a, b) => b.startedAt.localeCompare(a.startedAt));
}

function renderAtom(
  config: Required<Pick<BananapulseConfig, 'name' | 'domain'>>,
  mount: string,
  siteBase: string,
  incidents: Incident[],
): string {
  const updated = incidents[0]?.startedAt ?? new Date().toISOString();
  const base = siteBase.replace(/\/$/, '');
  const entries = incidents
    .map((i) => {
      const ts = i.resolvedAt ?? i.startedAt;
      return `  <entry>
    <id>tag:${escapeXml(config.domain)},2026:incident/${escapeXml(i.id)}</id>
    <title>${escapeXml(i.title)}</title>
    <link href="${base}${mount}/incidents#${escapeXml(i.id)}"/>
    <published>${i.startedAt}</published>
    <updated>${ts}</updated>
    <category term="${i.severity}"/>
    <content type="html">${escapeXml(i.body)}</content>
  </entry>`;
    })
    .join('\n');

  return `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>${escapeXml(config.name)} status incidents</title>
  <link href="${base}${mount}/incidents"/>
  <link rel="self" href="${base}${mount}/incidents.xml"/>
  <id>tag:${escapeXml(config.domain)},2026:incidents</id>
  <updated>${updated}</updated>
${entries}
</feed>
`;
}

// ── integration factory ─────────────────────────────────────────────────

export default function bananapulse(opts: BananapulseConfig): AstroIntegration {
  const mountPath = normaliseMount(opts.mountPath);
  const incidentsPath = opts.incidentsPath ?? DEFAULT_INCIDENTS_PATH;

  const VIRTUAL_ID = 'virtual:bananapulse/config';
  const RESOLVED_VIRTUAL_ID = '\0' + VIRTUAL_ID;

  let projectRoot = '';
  // virtualConfig is finalised inside config:setup once we know
  // projectRoot, then the absolute incidents path is baked in so the
  // routes don't have to guess process.cwd() at build time.
  let virtualConfigSource = 'export default null;';

  return {
    name: 'bananapulse',
    hooks: {
      'astro:config:setup': ({ injectRoute, updateConfig, config }) => {
        projectRoot = fileURLToPath(config.root);
        const absIncidentsPath = resolve(projectRoot, incidentsPath);

        const virtualConfig = {
          mountPath,
          name: opts.name,
          domain: opts.domain,
          sources: opts.sources,
          themeCssVarMap: opts.themeCssVarMap ?? {},
          themeCssPath: opts.themeCssPath ?? null,
          // Absolute path is computed once here so routes don't depend
          // on process.cwd() (which differs between `astro dev` and
          // build pipelines that run the build from a parent dir).
          absIncidentsPath,
        };
        virtualConfigSource = `export default ${JSON.stringify(virtualConfig)};`;

        // Inject status + incidents pages. Entrypoints resolve via the
        // package name so a consumer's node_modules/bananapulse/...
        // wins, exactly like @astrojs/starlight does it.
        injectRoute({
          pattern: mountPath === '/' ? '/' : mountPath,
          entrypoint: 'bananapulse/routes/index.astro',
        });
        injectRoute({
          pattern: mountPath === '/' ? '/incidents' : `${mountPath}/incidents`,
          entrypoint: 'bananapulse/routes/incidents.astro',
        });

        // Provide consumer config to the injected routes via a Vite
        // virtual module. The routes do `import config from
        // 'virtual:bananapulse/config'` and get the snapshot above.
        updateConfig({
          vite: {
            plugins: [
              {
                name: 'bananapulse:virtual-config',
                resolveId(id: string) {
                  if (id === VIRTUAL_ID) return RESOLVED_VIRTUAL_ID;
                  return null;
                },
                load(id: string) {
                  if (id !== RESOLVED_VIRTUAL_ID) return null;
                  return virtualConfigSource;
                },
              },
            ],
          },
        });
      },

      'astro:build:done': ({ dir, logger }) => {
        // Write incidents.xml into dist/<mountPath>/incidents.xml.
        // Static = no APIRoute = we materialise the feed once per build.
        const distDir = fileURLToPath(dir);
        const absIncidents = resolve(projectRoot, incidentsPath);
        const incidents = readIncidents(absIncidents);
        const siteBase = `https://${opts.domain}`;
        const atom = renderAtom(
          { name: opts.name, domain: opts.domain },
          mountPath,
          siteBase,
          incidents,
        );

        const outRel =
          mountPath === '/'
            ? 'incidents.xml'
            : join(mountPath.replace(/^\//, ''), 'incidents.xml');
        const outAbs = join(distDir, outRel);
        try {
          mkdirSync(dirname(outAbs), { recursive: true });
          writeFileSync(outAbs, atom, 'utf8');
          logger.info(
            `wrote ${incidents.length} incident(s) to ${outRel}`,
          );
        } catch (e) {
          logger.warn(`failed to write Atom feed: ${(e as Error).message}`);
        }
      },
    },
  };
}
