---
name: consume-proto-release
description: Upgrade the BSR-generated Go schema package after a specification Release and swap placeholder types. Use when a new liverty-music/schema version has been published to BSR and backend code must adopt the generated types.
---

### Consuming New Proto Types (after BSR gen)

When a specification Release has published new schema to BSR (see the
specification repo's AGENTS.md for the cross-repo release flow), upgrade and
adopt the generated types here:

```bash
# Pin to the released schema version
go get buf.build/gen/go/liverty-music/schema/...@vX.Y.Z
go mod tidy
make check
```

Then swap the placeholder types for the generated ones at each
`TODO: swap to generated type after BSR gen` marker and run `make check` again.
Open (or push) the PR only after this succeeds — do NOT open a draft PR before
BSR gen completes, as CI will fail on the missing types.
