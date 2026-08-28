import type { APIRoute } from 'astro';
import { getAllIncidents } from '../lib/db-incidents';
import { getMonitorProjection, pulpMonitorProjectionConfigured } from '../lib/pulp-bridge';
import { buildPulpIncidentHistory } from '../lib/pulp-monitor-projection';
import { SITE_TITLE, STATUS_DOMAIN } from '../pulse.config';
import { incidentStatusLabel } from '../lib/types';
import { severityLabel } from '../lib/skin-copy';
import type { Incident } from '../lib/types';

export const GET: APIRoute = async ({ locals }) => {
  const scope = (locals as any).scope as string | null;
  const ownerSelected = pulpMonitorProjectionConfigured();

  let incidents: Incident[] = [];
  try {
    incidents = ownerSelected
      ? buildPulpIncidentHistory(await getMonitorProjection(), scope, 20).incidents
      : await getAllIncidents({ product: scope || undefined, limit: 20 });
  } catch (_e) {
    if (ownerSelected) {
      return new Response('Live incident feed is temporarily unavailable.', {
        status: 503,
        headers: { 'Content-Type': 'text/plain; charset=utf-8' },
      });
    }
    // DB unreachable — return empty feed
  }

  const feedUrl = `https://${STATUS_DOMAIN}/feed.xml`;
  const siteUrl = `https://${STATUS_DOMAIN}`;

  const entries = incidents.map((inc) => {
    const updated = inc.resolved || inc.started;
    return `  <entry>
    <title>${escapeXml(inc.title)}</title>
    <id>${siteUrl}/incident/${inc.id}</id>
    <link href="${siteUrl}/incident/${inc.id}" />
    <updated>${new Date(updated).toISOString()}</updated>
    <summary>${escapeXml(severityLabel(inc.severity))} — ${escapeXml(incidentStatusLabel(inc.status))}</summary>
    <content type="text">${escapeXml(inc.timeline.length > 0 ? inc.timeline[inc.timeline.length - 1].body : inc.title)}</content>
  </entry>`;
  });

  const xml = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>${escapeXml(SITE_TITLE)}</title>
  <id>${feedUrl}</id>
  <link href="${siteUrl}" />
  <link href="${feedUrl}" rel="self" type="application/atom+xml" />
  <updated>${new Date().toISOString()}</updated>
${entries.join('\n')}
</feed>`;

  return new Response(xml, {
    status: 200,
    headers: {
      'Content-Type': 'application/atom+xml; charset=utf-8',
      'Cache-Control': 'public, max-age=300',
    },
  });
};

function escapeXml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;');
}
