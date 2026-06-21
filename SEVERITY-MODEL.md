# Status & Severity model — cheat sheet

Two **separate** vocabularies. Never mix them.

- **STATUS** = a component's own health → the node color/word: **UP | DEGRADED | DOWN** (+ MAINTENANCE).
  (Internal enum is still `operational | degraded | outage | maintenance`; UP/DOWN are just the labels.)
- **SEVERITY / LEVEL** = how much an incident *matters* → the incident badge: **MINOR | MODERATE | MAJOR**.

The engine figures severity out automatically from the **signal** a monitor reports
(`up | degraded | down`) and whether the component is flagged **critical** in the seed.

---

## The chart

```
 SIGNAL        →   STATUS (node word)    →   SEVERITY (incident, auto)
 (monitor)                                   non-critical ───────► critical
 ─────────────────────────────────────────────────────────────────────────
 up            →   UP        (green)     →   —  (no incident, all fine)
 degraded      →   DEGRADED  (yellow)    →   MINOR  ──────────►  MODERATE
 down          →   DOWN      (red)       →   MODERATE  ──────►   MAJOR
```

**Criticality is the slider.** Same signal, severity depends on blast radius:

| Signal ↓ \ Component → | non-critical | critical |
|------------------------|:------------:|:--------:|
| **degraded**           |  **MINOR**   | **MODERATE** |
| **down**               | **MODERATE** | **MAJOR**   |

> A non-essential service being down is *not* a max-severity event; an essential
> one being down is. That's the whole point of the slider.

---

## Confidence is a third, independent axis

Confidence does **not** change severity — it sets the incident's **status** (how sure we are):

```
 1 unconfirmed external vantage   →  "watch"        →  surfaced as MINOR, status INVESTIGATING, never pages
 1 trusted first-party vantage    →  declared       →  full severity, status INVESTIGATING
 ≥2 monitors agree                →  declared       →  full severity, status IDENTIFIED
```

So a lone external probe seeing `down` is **MINOR / investigating** until a second
vantage agrees — then it jumps to its true severity (MODERATE/MAJOR) and IDENTIFIED.

---

## Paging / email

- **MINOR** → visible on the page, **never emails** subscribers ("minor never pages").
- **MODERATE / MAJOR** → emails subscribers on open, severity change, and resolve.

## Where this lives in code

- `src/lib/quorum.ts` — `severityFor(ev, critical)` (the table above), `evaluateComponent` (signal→status), `reconcileIncident` (opens/tracks the incident).
- `src/lib/notify.ts` — `NOTIFY_SEVERITIES` (the minor-stays-quiet gate).
- `src/lib/components.ts` — rollup: a **critical** child's outage propagates uncapped to its parent; a non-critical child's outage is floored to *degraded* (the partial-outage floor).
- Manual incidents keep the **human's** stated level — never down-ranked by criticality.
