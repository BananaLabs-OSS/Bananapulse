import { db } from '@/db';
import { subscribers } from '@/db/schema';
import { eq, isNotNull } from 'drizzle-orm';
import { nanoid } from 'nanoid';
import { sendConfirmationEmail } from './email';
import { STATUS_DOMAIN } from '@/pulse.config';

export async function addSubscriber(email: string, confirmBaseUrl: string) {
  const normalized = email.toLowerCase().trim();
  const existing = await db.select().from(subscribers).where(eq(subscribers.email, normalized));
  if (existing.length > 0) {
    // Already exists — if unconfirmed, resend confirmation. Never reveal existence.
    const row = existing[0];
    if (!row.confirmedAt) {
      await sendConfirmationEmail(email, buildConfirmUrl(confirmBaseUrl, row.id));
    }
    return row;
  }
  const id = nanoid();
  const row = { id, email: normalized };
  await db.insert(subscribers).values(row);
  await sendConfirmationEmail(normalized, buildConfirmUrl(confirmBaseUrl, id));
  return row;
}

export async function confirmSubscriber(id: string): Promise<boolean> {
  const rows = await db.select().from(subscribers).where(eq(subscribers.id, id));
  if (rows.length === 0) return false;
  if (rows[0].confirmedAt) return true; // already confirmed
  await db.update(subscribers).set({ confirmedAt: new Date() }).where(eq(subscribers.id, id));
  return true;
}

export async function removeSubscriber(id: string): Promise<boolean> {
  const rows = await db.select().from(subscribers).where(eq(subscribers.id, id));
  if (rows.length === 0) return false;
  await db.delete(subscribers).where(eq(subscribers.id, id));
  return true;
}

export async function listSubscribers() {
  return db.select().from(subscribers).orderBy(subscribers.createdAt);
}

/** Only confirmed subscribers receive incident emails. */
export async function listConfirmedSubscribers() {
  return db.select().from(subscribers).where(isNotNull(subscribers.confirmedAt)).orderBy(subscribers.createdAt);
}

export function buildConfirmUrl(base: string, id: string): string {
  return `${base.replace(/\/$/, '')}/api/subscribe/confirm?token=${id}`;
}

export function buildUnsubscribeUrl(base: string, id: string): string {
  return `${base.replace(/\/$/, '')}/api/unsubscribe?token=${id}`;
}
