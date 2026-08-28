import type { APIRoute } from 'astro';
import { removeSubscriber } from '@/lib/subscribers';
import {
  pulpSubscriberLifecycleConfigured,
  subscriberTokenRequestID,
  unsubscribeWithOwner,
} from '@/lib/pulp-bridge';

/**
 * GET /api/unsubscribe?token=<subscriber-id>
 * Deletes the subscriber row. Link is included in all incident emails.
 */
export const GET: APIRoute = async ({ url }) => {
  const token = url.searchParams.get('token')?.trim() ?? '';

  if (!token) {
    return new Response(
      'Missing token.',
      { status: 400, headers: { 'Content-Type': 'text/plain' } },
    );
  }

  if (pulpSubscriberLifecycleConfigured()) {
    try {
      await unsubscribeWithOwner(subscriberTokenRequestID('unsubscribe', token), token);
    } catch {
      // Preserve the public endpoint's idempotent anti-enumeration contract.
    }
  } else {
    await removeSubscriber(token);
  }

  // Redirect to status page — idempotent (ok even if row was already gone).
  return new Response(null, {
    status: 302,
    headers: { Location: '/?unsubscribed=1' },
  });
};
