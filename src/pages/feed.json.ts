/**
 * GET /feed.json — incident feed in JSON Feed 1.1 format.
 *
 * Same incident data as /feed.atom, JSON shape for programmatic consumers.
 * Reuses getAllIncidents + the label helpers from types.ts.
 */
import type { APIRoute } from 'astro';
import { getAllIncidents } from '../lib/db-incidents';
import { SITE_TITLE, STATUS_DOMAIN } from '../pulse.config';
import { incidentStatusLabel } from '../lib/types';
import { severityLabel } from '../lib/skin-copy';
import type { Incident } from '../lib/types';

export const GET: APIRoute = async ({ locals }) => {
  const scope = (locals as any).scope as string | null;

  let incidents: Incident[] = [];
  try {
    incidents = await getAllIncidents({ product: scope || undefined, limit: 20 });
  } catch (_e) {
    // DB unreachable — empty feed
  }

  const siteUrl = `https://${STATUS_DOMAIN}`;

  const feed = {
    version: 'https://jsonfeed.org/version/1.1',
    title: SITE_TITLE,
    home_page_url: siteUrl,
    feed_url: `${siteUrl}/feed.json`,
    items: incidents.map((inc) => {
      const updated = inc.resolved || inc.started;
      const last = inc.timeline.length > 0 ? inc.timeline[inc.timeline.length - 1].body : inc.title;
      return {
        id: `${siteUrl}/incident/${inc.id}`,
        url: `${siteUrl}/incident/${inc.id}`,
        title: inc.title,
        content_text: last,
        summary: `${severityLabel(inc.severity)} — ${incidentStatusLabel(inc.status)}`,
        date_published: new Date(inc.started).toISOString(),
        date_modified: new Date(updated).toISOString(),
        _status: {
          severity: inc.severity,
          status: inc.status,
          affects: inc.affects ?? [],
          resolved: !!inc.resolved,
        },
      };
    }),
  };

  return new Response(JSON.stringify(feed, null, 2), {
    status: 200,
    headers: {
      'Content-Type': 'application/feed+json; charset=utf-8',
      'Cache-Control': 'public, max-age=300',
      'Access-Control-Allow-Origin': '*',
    },
  });
};
