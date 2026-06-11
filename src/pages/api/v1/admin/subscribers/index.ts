/**
 * Admin subscriber endpoints (admin-session auth). Mostly read.
 *   GET /api/v1/admin/subscribers   list
 */
import type { APIRoute } from 'astro';
import { requireAdmin, ok } from '@/lib/admin-api';
import { listSubscribers } from '@/lib/subscribers';

export const GET: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  return ok(await listSubscribers());
};
