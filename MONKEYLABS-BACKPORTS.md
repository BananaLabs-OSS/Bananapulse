# MonkeyLabs reuse audit

Compared the current `status` instance and its declared overlays with Bananapulse source-v1.0.8. MonkeyLabs remains an instance, not an upstream source fork. Uncommitted changes in both original checkouts were preserved.

## Included in Bananapulse

- Uptime-strip hover magnifier and selected-day incident cards.
- Touch selection/captions, readable current-day striped marker, hover-exit cleanup and reduced-motion handling.
- UTC day labels and maintenance-banner spacing.
- Keyboard navigation added during the port; selected-day links URI-encode IDs and embedded JSON escapes `<` to prevent incident text ending the script element.
- Data-owner failures return 503 with no-store and Retry-After instead of a misleading 404, including the legacy Postgres path.
- Indexed latest-observation, observed-component and sweep queries. The donor's predecessor seek verifies the complete source/component match so unrelated history does not leak. Real isolated PostgreSQL parity/index-seek tests were copied and passed.

## Keep in the instance

MonkeyLabs/Sessions branding, host scopes, Sessions maintenance alert integration, deployment adapter selection, known product probe targets and secret configuration are instance policy. The current Node scheduler hardcodes product targets and should not be copied into the generic engine wholesale. CDN caching needs deployment-specific review, especially for stale/unavailable status; it was not copied wholesale.

## Consumer follow-up

MonkeyLabs currently pins source-v1.0.0 locally and overlays SkinML, skins.css, quorum.ts and the catch-all page. Updating the source pin alone will not pick up changes in those overlaid paths. A composition PR must reconcile those overrides and run its compatibility checks. This audit does not merge or deploy MonkeyLabs.
