// Resend magic-link email — mono brand header, bordered card, accent button.
// From-address + brand come from pulse.config / env (RESEND_FROM_*).
// Generic transactional email styling.

import { COMPANY, SITE_TITLE, STATUS_DOMAIN } from '@/pulse.config';

function confirmationHtml(confirmUrl: string): string {
  return `<!DOCTYPE html>
<html lang="en" style="height:100%;margin:0;">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0"></head>
<body style="margin:0;padding:0;height:100%;background:#1a1815;font-family:'DM Sans',system-ui,-apple-system,sans-serif;color:#d8cdb4;-webkit-font-smoothing:antialiased;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#1a1815;height:100%;min-height:100vh;">
  <tr><td align="center" style="padding:48px 16px 0;vertical-align:top;">
    <table role="presentation" width="520" cellpadding="0" cellspacing="0" style="max-width:520px;width:100%;">
      <tr><td style="padding-bottom:24px;">
        <span style="font-family:'DM Mono','JetBrains Mono',monospace;font-size:13px;font-weight:500;color:#847a67;letter-spacing:.04em;text-transform:lowercase;">${COMPANY} status</span>
      </td></tr>
      <tr><td style="background:#221f1b;border:1px solid #2e2924;border-radius:4px;padding:28px 28px 24px;">
        <h1 style="margin:0 0 6px;font-family:'DM Sans',system-ui,sans-serif;font-size:20px;font-weight:600;color:#f0e8d4;letter-spacing:-.01em;">Confirm your subscription</h1>
        <p style="margin:0 0 18px;font-size:14px;line-height:1.55;color:#aca28c;">Click below to confirm you want to receive status updates for ${COMPANY}. This link expires in 24 hours.</p>
        <table role="presentation" cellpadding="0" cellspacing="0" style="margin:20px 0 4px;"><tr><td>
          <a href="${confirmUrl}" style="display:inline-block;padding:10px 24px;background:rgba(34,197,94,.10);color:#22c55e;font-size:13px;font-weight:600;text-decoration:none;border-radius:3px;border:1px solid rgba(34,197,94,.30);font-family:'DM Mono','JetBrains Mono',monospace;letter-spacing:.02em;">confirm subscription &rarr;</a>
        </td></tr></table>
        <p style="margin:18px 0 0;font-size:12px;line-height:1.5;color:#847a67;">If you didn&rsquo;t request this, ignore this email. You won&rsquo;t receive anything further.</p>
      </td></tr>
      <tr><td style="padding-top:16px;">
        <p style="margin:0;font-size:11px;line-height:1.5;color:#524a3e;font-family:'DM Mono','JetBrains Mono',monospace;word-break:break-all;">${confirmUrl}</p>
      </td></tr>
    </table>
  </td></tr>
  <tr><td align="center" style="padding:16px 16px 32px;vertical-align:bottom;">
    <p style="margin:0;font-family:'DM Mono','JetBrains Mono',monospace;font-size:10px;color:#524a3e;letter-spacing:.04em;">${STATUS_DOMAIN}</p>
  </td></tr>
</table>
</body>
</html>`;
}

export async function sendConfirmationEmail(to: string, confirmUrl: string) {
  const apiKey = process.env.RESEND_API_KEY;
  if (!apiKey) throw new Error('RESEND_API_KEY not set');

  const fromEmail = process.env.RESEND_FROM_EMAIL ?? `status@${STATUS_DOMAIN.replace(/^status\./, '')}`;
  const fromName = process.env.RESEND_FROM_NAME ?? SITE_TITLE;

  const res = await fetch('https://api.resend.com/emails', {
    method: 'POST',
    headers: {
      'Authorization': `Bearer ${apiKey}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      from: `${fromName} <${fromEmail}>`,
      to: [to],
      subject: `Confirm your ${SITE_TITLE} subscription`,
      text: `Confirm your subscription to receive status updates for ${COMPANY}:\n\n${confirmUrl}\n\nIf you didn't request this, ignore this email.`,
      html: confirmationHtml(confirmUrl),
    }),
  });

  if (!res.ok) {
    const body = await res.text();
    throw new Error(`Resend send failed: ${res.status} ${body}`);
  }
}

function magicLinkHtml(magicUrl: string): string {
  return `<!DOCTYPE html>
<html lang="en" style="height:100%;margin:0;">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0"></head>
<body style="margin:0;padding:0;height:100%;background:#1a1815;font-family:'DM Sans',system-ui,-apple-system,sans-serif;color:#d8cdb4;-webkit-font-smoothing:antialiased;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#1a1815;height:100%;min-height:100vh;">
  <tr><td align="center" style="padding:48px 16px 0;vertical-align:top;">
    <table role="presentation" width="520" cellpadding="0" cellspacing="0" style="max-width:520px;width:100%;">
      <tr><td style="padding-bottom:24px;">
        <span style="font-family:'DM Mono','JetBrains Mono',monospace;font-size:13px;font-weight:500;color:#847a67;letter-spacing:.04em;text-transform:lowercase;">${COMPANY} status</span>
      </td></tr>
      <tr><td style="background:#221f1b;border:1px solid #2e2924;border-radius:4px;padding:28px 28px 24px;">
        <h1 style="margin:0 0 6px;font-family:'DM Sans',system-ui,sans-serif;font-size:20px;font-weight:600;color:#f0e8d4;letter-spacing:-.01em;">Your admin login link</h1>
        <p style="margin:0 0 18px;font-size:14px;line-height:1.55;color:#aca28c;">Sign in to the ${SITE_TITLE} admin. This link expires in 15 minutes.</p>
        <table role="presentation" cellpadding="0" cellspacing="0" style="margin:20px 0 4px;"><tr><td>
          <a href="${magicUrl}" style="display:inline-block;padding:10px 24px;background:rgba(34,197,94,.10);color:#22c55e;font-size:13px;font-weight:600;text-decoration:none;border-radius:3px;border:1px solid rgba(34,197,94,.30);font-family:'DM Mono','JetBrains Mono',monospace;letter-spacing:.02em;">sign in &rarr;</a>
        </td></tr></table>
        <p style="margin:18px 0 0;font-size:12px;line-height:1.5;color:#847a67;">If you didn&rsquo;t request this, ignore this email. The link will expire on its own.</p>
      </td></tr>
      <tr><td style="padding-top:16px;">
        <p style="margin:0;font-size:11px;line-height:1.5;color:#524a3e;font-family:'DM Mono','JetBrains Mono',monospace;word-break:break-all;">${magicUrl}</p>
      </td></tr>
    </table>
  </td></tr>
  <tr><td align="center" style="padding:16px 16px 32px;vertical-align:bottom;">
    <p style="margin:0;font-family:'DM Mono','JetBrains Mono',monospace;font-size:10px;color:#524a3e;letter-spacing:.04em;">${STATUS_DOMAIN}</p>
  </td></tr>
</table>
</body>
</html>`;
}

export async function sendMagicLinkEmail(to: string, magicUrl: string) {
  const apiKey = process.env.RESEND_API_KEY;
  if (!apiKey) throw new Error('RESEND_API_KEY not set');

  const fromEmail = process.env.RESEND_FROM_EMAIL ?? `status@${STATUS_DOMAIN.replace(/^status\./, '')}`;
  const fromName = process.env.RESEND_FROM_NAME ?? SITE_TITLE;

  const res = await fetch('https://api.resend.com/emails', {
    method: 'POST',
    headers: {
      'Authorization': `Bearer ${apiKey}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      from: `${fromName} <${fromEmail}>`,
      to: [to],
      subject: 'Your admin login link',
      text: `Sign in to ${SITE_TITLE}:\n\n${magicUrl}\n\nThis link expires in 15 minutes. If you didn't request this, ignore this email.`,
      html: magicLinkHtml(magicUrl),
    }),
  });

  if (!res.ok) {
    const body = await res.text();
    throw new Error(`Resend send failed: ${res.status} ${body}`);
  }
}

/**
 * Generic transactional send via Resend, used by the incident notification
 * fan-out (notify.ts). Same sender identity and API as the magic-link sender;
 * caller supplies the rendered subject/text/html.
 */
export async function sendIncidentEmail(to: string, subject: string, text: string, html: string) {
  const apiKey = process.env.RESEND_API_KEY;
  if (!apiKey) throw new Error('RESEND_API_KEY not set');

  const fromEmail = process.env.RESEND_FROM_EMAIL ?? `status@${STATUS_DOMAIN.replace(/^status\./, '')}`;
  const fromName = process.env.RESEND_FROM_NAME ?? SITE_TITLE;

  const res = await fetch('https://api.resend.com/emails', {
    method: 'POST',
    headers: {
      'Authorization': `Bearer ${apiKey}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      from: `${fromName} <${fromEmail}>`,
      to: [to],
      subject,
      text,
      html,
    }),
  });

  if (!res.ok) {
    const body = await res.text();
    throw new Error(`Resend send failed: ${res.status} ${body}`);
  }
}
