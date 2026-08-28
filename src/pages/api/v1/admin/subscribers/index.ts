/**
 * Admin subscriber endpoints (admin-session auth). Mostly read.
 *   GET /api/v1/admin/subscribers   list
 */
import type { APIRoute } from 'astro';
import { requireAdmin, ok } from '@/lib/admin-api';
import { listSubscribers } from '@/lib/subscribers';
import {
  listSubscribersWithOwner,
  pulpOwnerRouteFamilyConfigured,
} from '@/lib/pulp-bridge';

export const GET: APIRoute = async (ctx) => {
  const who = await requireAdmin(ctx);
  if (who instanceof Response) return who;
  if (pulpOwnerRouteFamilyConfigured('subscriber-admin')) {
    const result = await listSubscribersWithOwner();
    return ok(result.subscribers.map(({ state: _state, ...subscriber }) => subscriber));
  }
  return ok(await listSubscribers());
};
