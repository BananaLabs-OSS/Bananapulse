# Versioned source artifacts

Bananapulse instances consume an OCI source image rather than merging this
repository's Git history. The image is intentionally runtime-neutral: it is an
immutable filesystem artifact containing the engine source at one reviewed Git
revision. Each instance overlays only its declared profile seam and performs its
own normal build.

## Release

1. Land and verify the engine change on `master`.
2. Create an annotated `source-vX.Y.Z` tag on that exact commit.
3. The `Publish source image` workflow reruns tests, type checks, and the local
   Node build before publishing two OCI tags to GHCR: the human release tag and
   `sha-<full commit>`.
4. Resolve the OCI digest and pin the downstream instance to
   `ghcr.io/bananalabs-oss/bananapulse-source@sha256:<digest>`.

Release tags are discovery aliases. Deployments pin the digest, so moving or
deleting a tag cannot silently alter an existing build. Rollback is a one-line
lock-file change to the previously tested digest.

The artifact contains committed public source only. It must never contain an
`.env`, credential, generated database, dependency directory, or build output.
