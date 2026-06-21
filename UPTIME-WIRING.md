# Wiring UptimeRobot → the status page (plain-English checklist)

This makes UptimeRobot an automatic **vantage** that feeds the status page's
quorum engine — so when UptimeRobot sees something down, the status page can
react instead of just emailing you.

UptimeRobot is an **untrusted** source: on its own it only puts a component on
WATCH (not public). It needs a second vantage (e.g. an on-box reporter) to agree before an
incident goes public. That's the abuse guard — don't expect a single UptimeRobot
alert to post a public outage by itself.

Nothing here is urgent. The code is already deployed and ready; these are the
one-time dashboard steps to turn the feed on (~10 minutes).

---

## Step 1 — Make up a secret

Pick any long random string (a throwaway passphrase / password-manager value).
This is the shared secret so nobody can forge fake "down" alerts at your status
page. Keep it somewhere you can paste it twice (Steps 2 and 3).

## Step 2 — Put the secret into Netlify

The status page is hosted on Netlify. Add the secret as an environment variable:

1. Netlify → your status site → **Site configuration → Environment variables**.
2. **Add a variable**:
   - Key: `UPTIME_HOOK_SECRET`
   - Value: the string from Step 1
3. Save, then **trigger a redeploy** (Deploys → Trigger deploy) so the new value
   is live. (Until this is set, the webhook endpoint returns 503 = "not
   configured" — which is the safe default.)

## Step 3 — Point each UptimeRobot monitor at the status page

In UptimeRobot, you need a **webhook alert contact**, then attach it to your
monitors.

1. UptimeRobot → **My Settings → Alert Contacts → Add Alert Contact**.
2. Type: **Webhook**.
3. URL to Notify (put your Step-1 secret after `key=`):

   ```
   https://status.example.com/api/v1/ingest/uptimerobot?key=YOUR-SECRET-HERE
   ```

4. Choose **POST** and **send as JSON** (a.k.a. "custom JSON / raw"), and set the
   POST value (the message body) to exactly this:

   ```json
   {"monitorID":"*monitorID*","monitorFriendlyName":"*monitorFriendlyName*","alertType":"*alertType*","alertDateTime":"*alertDateTime*","alertDetails":"*alertDetails*"}
   ```

   The `*...*` tokens are UptimeRobot variables — it fills them in automatically.
5. Save the alert contact, then **enable it on each monitor** you want feeding the
   status page (each monitor's settings → Alert Contacts To Notify → tick it).

## Step 4 — Map each monitor to a status-page component (in the admin)

The status page needs to know which monitor maps to which service. Use the
**mapping** approach (the reusable way — name your monitors anything you like):

1. Status admin → **Sources & Tokens**.
2. Under **Label → component mappings**, pick the **UptimeRobot** source, enter
   the monitor's **`monitorID`** as the *Raw label* (find it in the UptimeRobot
   monitor's URL/details), and choose the **Component** from the dropdown.
3. **Add mapping.** Re-adding the same label just replaces it; **Remove** deletes
   it. Repeat per monitor.

Tip: map the numeric `monitorID` (it survives renames — recommended). If you skip
mapping entirely, the adapter falls back to treating the monitor's friendly name
as the component id.

---

## How to check it's working

- **Force a test:** pause/unpause a monitor, or temporarily point one at a URL
  that 404s, so UptimeRobot fires a "down" then "up".
- **Look at the status admin** "live signals" / observations — you should see an
  `UptimeRobot` source appear with the right signal.
- The component goes to WATCH on a lone UptimeRobot signal; it only goes public
  if a second vantage agrees (by design).

## If something's off

- **401 Unauthorized** → the `key=` in the URL doesn't match `UPTIME_HOOK_SECRET`
  on Netlify (or the env var isn't deployed yet). Re-check Steps 1–2.
- **422 unmapped_target** → the monitor doesn't map to a component. Do Step 4
  (rename to match a component id, or add a mapping row).
- **503 not_configured** → `UPTIME_HOOK_SECRET` isn't set/deployed on Netlify.
- **Nothing happens at all** → confirm the alert contact is actually ticked on the
  monitor, and that POST/JSON (not the default query-string mode) is selected.

---

## What the code already handles for you

- **Native UptimeRobot payload** (`alertType` 1=down / 2=up / 3=ssl-informational).
- **`?key=` auth** so you don't need custom headers (UptimeRobot free tier can't
  always set them); a header `X-Uptime-Hook-Secret` also works.
- **Idempotency** — UptimeRobot retries are deduped (same monitor+state+time).
- **Observed time** — uses UptimeRobot's own `alertDateTime`, so delayed/retried
  webhooks order correctly and can't clobber newer readings.
- **Dead-man** — an UptimeRobot observation expires after ~10 min, so a probe that
  goes silent goes stale instead of pinning the last reading forever.
- **Back-compat** — the old `{ "service_id", "status" }` shape still works.

(Grafana Cloud uses the sibling endpoint `/api/v1/ingest/grafana`, same `?key=`
idea, blocked only on creating the Grafana Cloud account.)
