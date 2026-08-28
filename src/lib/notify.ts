/**
 * Incident notification fan-out (email channel only).
 *
 * The engine (quorum.ts) owns whether an incident *exists*; this module owns
 * *telling people about it*. It is called from the incident lifecycle callers
 * — the ingest / adapter path and the admin incident endpoints — NEVER from
 * inside quorum.ts, so the engine logic stays untouched and side-effect-free.
 *
 * Public surface:
 *   notifyIncident(incidentId, kind)      send one fan-out email for an incident
 *   snapshotComponent(componentId)        capture open-incident state pre-engine
 *   notifyForComponent(componentId, snap) diff against the snapshot, fire once
 *
 * Channel: email only, via the existing Resend integration in email.ts. There
 * is NO Discord channel (the founder does not use Discord). Recipients are the
 * rows in `subscribers` (the only subscription channel the schema models);
 * everyone subscribed gets every product's incidents — relevance is encoded in
 * the per-incident subject/body, not in a per-channel filter (which the schema
 * has no column for).
 *
 * Idempotency: the engine touches an incident on EVERY observation, but a
 * status/level rarely changes. notifyForComponent only emits when the derived
 * open-incident identity, severity, or status actually moved between the
 * pre-engine snapshot and the post-engine read — so a steady stream of "still
 * down" observations does not re-send. notifyIncident itself is a raw send and
 * is meant to be called from explicit human actions (a posted update), which
 * are intentional one-shots.
 */
import { db } from '@/db';
import { incidents, incidentTimeline, components, maintenance } from '@/db/schema';
import { eq, ne, desc, and, isNull } from 'drizzle-orm';
import { sendIncidentEmail } from './email';
import { listConfirmedSubscribers, buildUnsubscribeUrl } from './subscribers';
import { UMBRELLA_ID, COMPANY, STATUS_DOMAIN, scopeBrand } from '@/pulse.config';
import { openIncidentFor } from './quorum';

/** What kind of lifecycle event we are narrating to subscribers. */
export type NotifyKind = 'opened' | 'update' | 'resolved';

export interface IncidentSnapshot {
  /** open (non-resolved) incident id for the component, or null. */
  incidentId: string | null;
  status: string | null;
  severity: string | null;
}

/* ── snapshot / diff (the engine-path hook) ──────────────────── */

/**
 * Capture the open-incident state for a component BEFORE the engine runs, so
 * the caller can later diff and decide what (if anything) to narrate. Cheap;
 * one indexed read.
 */
export async function snapshotComponent(componentId: string): Promise<IncidentSnapshot> {
  const open = await openIncidentFor(componentId);
  return {
    incidentId: open?.id ?? null,
    status: open?.status ?? null,
    severity: open?.severity ?? null,
  };
}

/**
 * Snapshot the open-incident state of EVERY component that currently has an
 * open (non-resolved) incident. Used by the sweep/dead-man path to capture
 * "before" state ahead of a quorum sweep that may auto-resolve incidents, so
 * those resolutions can be narrated. Components not in the returned map have no
 * open incident; the caller defaults them to an empty snapshot, which lets a
 * sweep-driven *open* still narrate as 'opened'.
 */
export async function snapshotAllOpenIncidents(): Promise<Map<string, IncidentSnapshot>> {
  const rows = await db.select().from(incidents).where(ne(incidents.status, 'resolved'));
  const map = new Map<string, IncidentSnapshot>();
  for (const inc of rows) {
    const cid = inc.affects[0];
    if (!cid) continue;
    // One snapshot per component; if multiple incidents share a component the
    // first (any) is sufficient for transition detection.
    if (!map.has(cid)) {
      map.set(cid, { incidentId: inc.id, status: inc.status, severity: inc.severity });
    }
  }
  return map;
}

/**
 * Diff the component's open-incident state against a pre-engine snapshot and
 * fan out exactly one email for the transition that occurred:
 *
 *   none -> open                 'opened'
 *   open -> resolved/none        'resolved'
 *   open -> open, severity moved  'update'
 *
 * No change → no email (idempotent against a steady stream of observations).
 * Best-effort: notification failure is logged, never thrown, so it can never
 * break the ingest response or the engine's truth.
 */
// Only CONFIRMED-impact incidents email subscribers. MINOR (a single unconfirmed
// vantage, or a non-critical degradation) is surfaced on the page but stays quiet
// — "minor never pages." Crossing INTO moderate/major notifies as 'opened';
// dropping below it (resolved, or downgraded to minor) reads as 'resolved'.
const NOTIFY_SEVERITIES = new Set(['moderate', 'major']);
function notifiable(snap: IncidentSnapshot): boolean {
  return !!snap.incidentId && NOTIFY_SEVERITIES.has(snap.severity ?? '');
}

export async function notifyForComponent(componentId: string, before: IncidentSnapshot): Promise<void> {
  try {
    const after = await snapshotComponent(componentId);
    const wasNotable = notifiable(before);
    const isNotable = notifiable(after);

    // Crossed INTO notable (brand-new, or a minor escalated to moderate/major).
    if (!wasNotable && isNotable) {
      await notifyIncident(after.incidentId!, 'opened');
      return;
    }

    // Dropped BELOW notable (resolved, or downgraded to a quiet minor).
    if (wasNotable && !isNotable) {
      await notifyIncident(before.incidentId!, 'resolved');
      return;
    }

    // Both notable, same incident: only narrate a real severity change (moderate <-> major).
    if (
      wasNotable && isNotable &&
      before.incidentId === after.incidentId &&
      before.severity !== after.severity
    ) {
      await notifyIncident(after.incidentId!, 'update');
    }
  } catch (e) {
    console.error('[notify] notifyForComponent failed', e);
  }
}

/* ── the named fan-out entry point ───────────────────────────── */

/**
 * Send one fan-out email for an incident, templated by `kind`. Loads the
 * incident, resolves the affected product (for scope-aware copy), renders the
 * subject/body, and emails every subscriber. Best-effort and idempotent-ish:
 * call it on a genuine lifecycle event (open / severity change / resolve / a
 * human-posted update), not on every observation.
 */
export async function notifyIncident(incidentId: string, kind: NotifyKind): Promise<void> {
  try {
    const rows = await db.select().from(incidents).where(eq(incidents.id, incidentId));
    const inc = rows[0];
    if (!inc) return;

    // Only confirmed subscribers receive emails (double opt-in).
    const recipients = await listConfirmedSubscribers();
    if (recipients.length === 0) return;

    const scope = await resolveScopeName(inc.affects);
    const latest = await latestUpdateBody(incidentId);
    const base = process.env.PUBLIC_STATUS_URL || `https://${STATUS_DOMAIN}`;

    // Fan out. Sequential keeps it gentle on the Resend rate limit and avoids a
    // partial-failure storm; subscriber lists are small at this stage.
    for (const r of recipients) {
      try {
        const unsubscribeUrl = buildUnsubscribeUrl(base, r.id);
        const { subject, text, html } = renderIncidentEmail({
          kind,
          scope,
          title: inc.title,
          severity: inc.severity,
          status: inc.status,
          body: latest ?? inc.summary,
          incidentUrl: incidentUrl(inc.id),
          unsubscribeUrl,
        });
        await sendIncidentEmail(r.email, subject, text, html);
      } catch (e) {
        console.error('[notify] send failed for', r.email, e);
      }
    }
  } catch (e) {
    console.error('[notify] notifyIncident failed', e);
  }
}

/**
 * Email confirmed subscribers about a maintenance window. `kind`:
 *  - 'announced' when it's created (advertise ahead)
 *  - 'started' / 'ended' when the window opens/closes (needs a sweep to fire).
 * Best-effort + sequential, mirroring notifyIncident.
 */
export async function notifyMaintenance(maintId: string, kind: 'announced' | 'started' | 'ended'): Promise<void> {
  try {
    const rows = await db.select().from(maintenance).where(eq(maintenance.id, maintId));
    const m = rows[0];
    if (!m) return;
    const recipients = await listConfirmedSubscribers();
    if (recipients.length === 0) return;
    const scope = await resolveScopeName(m.affects);
    const base = process.env.PUBLIC_STATUS_URL || `https://${STATUS_DOMAIN}`;
    for (const r of recipients) {
      try {
        const unsubscribeUrl = buildUnsubscribeUrl(base, r.id);
        const { subject, text, html } = renderMaintenanceEmail({
          kind, scope, title: m.title, body: m.summary,
          emergency: ((m as { kind?: string }).kind ?? 'scheduled') === 'emergency',
          start: m.scheduledStart, end: m.scheduledEnd,
          statusUrl: base.replace(/\/$/, ''), unsubscribeUrl,
        });
        await sendIncidentEmail(r.email, subject, text, html);
      } catch (e) {
        console.error('[notify] maintenance send failed for', r.email, e);
      }
    }
  } catch (e) {
    console.error('[notify] notifyMaintenance failed', e);
  }
}

/** Templated subject/body for a maintenance window. */
export function renderMaintenanceEmail(opts: {
  kind: 'announced' | 'started' | 'ended';
  scope: string;
  title: string;
  body: string;
  emergency: boolean;
  start: Date;
  end: Date;
  statusUrl: string;
  unsubscribeUrl?: string;
}): { subject: string; text: string; html: string } {
  const { kind, scope, title, body, emergency, start, end, statusUrl, unsubscribeUrl } = opts;
  const label = emergency ? 'Emergency maintenance' : 'Scheduled maintenance';
  const when = `${start.toUTCString()} – ${end.toUTCString()}`;
  let subject: string;
  let lead: string;
  switch (kind) {
    case 'announced':
      subject = `[${scope}] ${label}: ${title}`;
      lead = `${emergency ? 'Emergency' : 'Scheduled'} maintenance is planned for ${scope}.`;
      break;
    case 'started':
      subject = `[${scope}] Maintenance started: ${title}`;
      lead = `Maintenance on ${scope} has begun.`;
      break;
    case 'ended':
      subject = `[${scope}] Maintenance complete: ${title}`;
      lead = `Maintenance on ${scope} is complete. Thanks for your patience.`;
      break;
  }
  const unsubLine = unsubscribeUrl ? `\nUnsubscribe: ${unsubscribeUrl}` : '';
  const text = `${lead}\n\nWhen: ${when}\n\n${body}\n\nStatus: ${statusUrl}\n\n— ${scope} Status\nYou're receiving this because you subscribed to status updates.${unsubLine}`;
  const html = incidentEmailHtml({
    scope, title: `${label}: ${title}`, lead,
    body: `${body}\n\nWhen: ${when}`,
    incidentUrl: statusUrl, resolved: kind === 'ended', unsubscribeUrl,
  });
  return { subject, text, html };
}

/* ── copy + scope helpers ────────────────────────────────────── */

/** The latest timeline body for an incident (the freshest narration). */
async function latestUpdateBody(incidentId: string): Promise<string | null> {
  const rows = await db.select({ body: incidentTimeline.body }).from(incidentTimeline)
    .where(eq(incidentTimeline.incidentId, incidentId))
    .orderBy(desc(incidentTimeline.at))
    .limit(1);
  return rows[0]?.body ?? null;
}

/**
 * Resolve a human scope label by walking the first affected component up the
 * components tree to its nearest product (else organization) ancestor — the
 * SAME single model the public surface uses. Falls back to the umbrella scope,
 * never a hard-coded product.
 */
async function resolveScopeName(affects: string[]): Promise<string> {
  if (!affects || affects.length === 0) return COMPANY;
  const rows = await db.select({ id: components.id, parentId: components.parentId, kind: components.kind })
    .from(components).where(isNull(components.archivedAt));
  const byId = new Map(rows.map((r) => [r.id, r]));
  let cur = byId.get(affects[0]);
  let product: string | null = null;
  let orgFallback: string | null = null;
  while (cur) {
    if (cur.kind === 'product') { product = cur.id; break; }
    if (cur.kind === 'organization') orgFallback = cur.id;
    cur = cur.parentId ? byId.get(cur.parentId) : undefined;
  }
  const key = product ?? orgFallback ?? UMBRELLA_ID;
  if (key === UMBRELLA_ID) return COMPANY;
  // Resolve the display name through the seam, never re-derive it here.
  return scopeBrand(key).wordmark;
}

/** Public incident permalink, scoped to the status site. */
function incidentUrl(id: string): string {
  const base = process.env.PUBLIC_STATUS_URL || `https://${STATUS_DOMAIN}`;
  return `${base.replace(/\/$/, '')}/incidents/${id}`;
}

function severityWord(severity: string): string {
  if (severity === 'major') return 'a major outage';
  if (severity === 'moderate') return 'degraded performance';
  return 'an issue';
}

/** Templated subject/body per kind + scope. */
export function renderIncidentEmail(opts: {
  kind: NotifyKind;
  scope: string;
  title: string;
  severity: string;
  status: string;
  body: string;
  incidentUrl: string;
  unsubscribeUrl?: string;
}): { subject: string; text: string; html: string } {
  const { kind, scope, title, severity, body, incidentUrl, unsubscribeUrl } = opts;

  let subject: string;
  let lead: string;
  switch (kind) {
    case 'opened':
      subject = `[${scope}] Investigating: ${title}`;
      lead = `We're investigating ${severityWord(severity)} affecting ${scope}.`;
      break;
    case 'update':
      subject = `[${scope}] Update: ${title}`;
      lead = `There's an update on an ongoing issue affecting ${scope}.`;
      break;
    case 'resolved':
      subject = `[${scope}] Resolved: ${title}`;
      lead = `The issue affecting ${scope} has been resolved.`;
      break;
  }

  const unsubLine = unsubscribeUrl ? `\nUnsubscribe: ${unsubscribeUrl}` : '';
  const text = `${lead}\n\n${body}\n\nFollow along: ${incidentUrl}\n\n— ${scope} Status\nYou're receiving this because you subscribed to status updates.${unsubLine}`;
  const html = incidentEmailHtml({ scope, title, lead, body, incidentUrl, resolved: kind === 'resolved', unsubscribeUrl });
  return { subject, text, html };
}

/** HTML-escape a free-text value before interpolating it into the email body. */
function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/** HTML email in the status system's TUI dialect (mirrors email.ts magic-link). */
function incidentEmailHtml(opts: {
  scope: string;
  title: string;
  lead: string;
  body: string;
  incidentUrl: string;
  resolved: boolean;
  unsubscribeUrl?: string;
}): string {
  const { scope, title, lead, body, incidentUrl, resolved, unsubscribeUrl } = opts;
  const accent = resolved ? '#22c55e' : '#f59e0b';
  const accentBg = resolved ? 'rgba(34,197,94,.10)' : 'rgba(245,158,11,.10)';
  const accentBorder = resolved ? 'rgba(34,197,94,.30)' : 'rgba(245,158,11,.30)';
  return `<!DOCTYPE html>
<html lang="en" style="height:100%;margin:0;">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0"></head>
<body style="margin:0;padding:0;height:100%;background:#1a1815;font-family:'DM Sans',system-ui,-apple-system,sans-serif;color:#d8cdb4;-webkit-font-smoothing:antialiased;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#1a1815;height:100%;min-height:100vh;">
  <tr><td align="center" style="padding:48px 16px 0;vertical-align:top;">
    <table role="presentation" width="520" cellpadding="0" cellspacing="0" style="max-width:520px;width:100%;">
      <tr><td style="padding-bottom:24px;">
        <span style="font-family:'DM Mono','JetBrains Mono',monospace;font-size:13px;font-weight:500;color:#847a67;letter-spacing:.04em;text-transform:lowercase;">${escapeHtml(scope.toLowerCase())} status</span>
      </td></tr>
      <tr><td style="background:#221f1b;border:1px solid #2e2924;border-radius:4px;padding:28px 28px 24px;">
        <h1 style="margin:0 0 6px;font-family:'DM Sans',system-ui,sans-serif;font-size:20px;font-weight:600;color:#f0e8d4;letter-spacing:-.01em;">${escapeHtml(title)}</h1>
        <p style="margin:0 0 14px;font-size:14px;line-height:1.55;color:#aca28c;">${escapeHtml(lead)}</p>
        <p style="margin:0 0 18px;font-size:14px;line-height:1.6;color:#d8cdb4;">${escapeHtml(body)}</p>
        <table role="presentation" cellpadding="0" cellspacing="0" style="margin:8px 0 4px;"><tr><td>
          <a href="${incidentUrl}" style="display:inline-block;padding:10px 24px;background:${accentBg};color:${accent};font-size:13px;font-weight:600;text-decoration:none;border-radius:3px;border:1px solid ${accentBorder};font-family:'DM Mono','JetBrains Mono',monospace;letter-spacing:.02em;">view status &rarr;</a>
        </td></tr></table>
      </td></tr>
      <tr><td style="padding-top:16px;">
        <p style="margin:0;font-size:11px;line-height:1.5;color:#524a3e;font-family:'DM Mono','JetBrains Mono',monospace;word-break:break-all;">${incidentUrl}</p>
      </td></tr>
    </table>
  </td></tr>
  <tr><td align="center" style="padding:16px 16px 32px;vertical-align:bottom;">
    <p style="margin:0;font-family:'DM Mono','JetBrains Mono',monospace;font-size:10px;color:#524a3e;letter-spacing:.04em;">You&rsquo;re receiving this because you subscribed to status updates.${unsubscribeUrl ? ` &middot; <a href="${unsubscribeUrl}" style="color:#524a3e;text-decoration:underline;">unsubscribe</a>` : ''}</p>
  </td></tr>
</table>
</body>
</html>`;
}
