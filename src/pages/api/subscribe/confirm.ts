import type { APIRoute } from 'astro';
import { confirmSubscriber } from '@/lib/subscribers';
import {
  confirmWithOwner,
  pulpSubscriberLifecycleConfigured,
  subscriberTokenRequestID,
} from '@/lib/pulp-bridge';

/**
 * GET /api/subscribe/confirm?token=<subscriber-id>
 * Sets confirmedAt for the subscriber row. Double opt-in: only confirmed
 * subscribers receive incident emails.
 */
export const GET: APIRoute = async ({ url }) => {
  const token = url.searchParams.get('token')?.trim() ?? '';

  if (!token) {
    return new Response(
      'Missing token.',
      { status: 400, headers: { 'Content-Type': 'text/plain' } },
    );
  }

  let ok = false;
  if (pulpSubscriberLifecycleConfigured()) {
    try {
      const result = await confirmWithOwner(subscriberTokenRequestID('confirm', token), token);
      ok = result.confirmed === true;
    } catch {
      ok = false;
    }
  } else {
    ok = await confirmSubscriber(token);
  }

  if (!ok) {
    return new Response(
      'Confirmation link not found or already used.',
      { status: 404, headers: { 'Content-Type': 'text/plain' } },
    );
  }

  // Redirect to status page with a success message.
  return new Response(null, {
    status: 302,
    headers: { Location: '/?subscribed=1' },
  });
};
