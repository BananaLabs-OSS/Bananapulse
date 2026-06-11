/**
 * DELETE /api/v1/admin/subscribers/:id — remove a subscriber endpoint.
 */
import type { APIRoute } from 'astro';
import { db } from '@/db';
import { subscribers } from '@/db/schema';
import { eq } from 'drizzle-orm';
import { requireAdmin, ok } from '@/lib/admin-api';

export const DELETE: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  await db.delete(subscribers).where(eq(subscribers.id, ctx.params.id!));
  return ok({ deleted: true });
};
