import type { APIRoute } from 'astro';
import { createMagicLink, issueMagicLinkWithOwner } from '@/lib/admin-auth';
import { sendMagicLinkEmail } from '@/lib/email';
import { pulpOwnerRouteFamilyConfigured } from '@/lib/pulp-bridge';

export const POST: APIRoute = async ({ request, url }) => {
  try {
    const body = await request.json();
    const email = body?.email?.toLowerCase?.()?.trim?.();
    const adminEmail = process.env.ADMIN_EMAIL?.toLowerCase?.()?.trim?.();

    // Always return same response shape (anti-enumeration)
    const okResponse = new Response(
      JSON.stringify({ data: { sent: true } }),
      { status: 200, headers: { 'Content-Type': 'application/json' } }
    );

    if (!email || !adminEmail || email !== adminEmail) {
      return okResponse;
    }

    const ownerEnabled = pulpOwnerRouteFamilyConfigured('auth');
    const ownerResult = ownerEnabled ? await issueMagicLinkWithOwner(email) : null;
    // The auth owner intentionally returns `deliver: false` for an unknown or
    // disabled identity. Keep the public response indistinguishable.
    if (ownerEnabled && !ownerResult?.deliver) return okResponse;

    const token = ownerResult?.token ?? await createMagicLink(email);
    const magicUrl = `${url.origin}/admin/verify?token=${token}`;
    await sendMagicLinkEmail(email, magicUrl);
    return okResponse;
  } catch {
    return new Response(JSON.stringify({ error: { code: 'server_error', message: 'Something went wrong.' } }), { status: 500 });
  }
};
