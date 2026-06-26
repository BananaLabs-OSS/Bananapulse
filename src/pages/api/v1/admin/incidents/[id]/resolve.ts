/**
 * POST /api/v1/admin/incidents/:id/resolve — force-resolve.
 *
 * Routes through recordManualOverride with signal 'ok' so the resolution flows
 * through the same engine (the manual source clears its non-ok read and the
 * incident is closed with a human-authored RESOLVED update). We do not hand-set
 * status='resolved' on the row directly.
 */
import type { APIRoute } from 'astro';
import { db } from '@/db';
import { incidents } from '@/db/schema';
import { eq } from 'drizzle-orm';
import { requireAdmin, ok, err } from '@/lib/admin-api';
import { getManualSource } from '@/lib/sources';
import { recordManualOverride } from '@/lib/quorum';
import { snapshotComponent, notifyForComponent } from '@/lib/notify';

export const POST: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  const id = ctx.params.id!;
  const rows = await db.select().from(incidents).where(eq(incidents.id, id));
  const inc = rows[0];
  if (!inc) return err('not_found', 'Incident not found.', 404);

  const body = await ctx.request.json().catch(() => ({}));
  const manual = await getManualSource();
  // Loop over ALL affected components so multi-component incidents are fully
  // cleared and don't leave zombie re-open state on the next sweep.
  // Mirrors the public PATCH /api/v1/incidents/:id resolve path.
  for (const componentId of inc.affects) {
    const before = await snapshotComponent(componentId);
    await recordManualOverride({
      manualSourceId: manual.id,
      componentId,
      signal: 'ok',
      body: body?.body || `Resolved by ${who}.`,
      author: who,
    });
    await notifyForComponent(componentId, before);
  }

  const updated = await db.select().from(incidents).where(eq(incidents.id, id));
  return ok(updated[0]);
};
