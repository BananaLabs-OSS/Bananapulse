/**
 * DELETE /api/v1/admin/subscribers/:id — remove a subscriber endpoint.
 */
import type { APIRoute } from 'astro';
import { db } from '@/db';
import { subscribers } from '@/db/schema';
import { eq } from 'drizzle-orm';
import { requireAdmin, ok } from '@/lib/admin-api';
import {
  deleteSubscriberWithOwner,
  pulpOwnerRequestID,
  pulpOwnerRouteFamilyConfigured,
} from '@/lib/pulp-bridge';

export const DELETE: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  if (pulpOwnerRouteFamilyConfigured('subscriber-admin')) {
    const id = ctx.params.id!;
    await deleteSubscriberWithOwner(pulpOwnerRequestID('subscriber-admin-delete', id), id);
    return ok({ deleted: true });
  }
  await db.delete(subscribers).where(eq(subscribers.id, ctx.params.id!));
  return ok({ deleted: true });
};
