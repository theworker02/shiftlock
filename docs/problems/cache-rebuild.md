# Rebuild a cache without partial activation

**Problem:** A cache rebuild must not serve a half-built generation.

**Approach:** Use `resource/cache.RunGenerationFlow`:

1. **Reserve** — `BuildGeneration` marks a build in progress
2. **Build** — populate seed offline via app `Build` func
3. **Verify** — optional `Verify` before activation
4. **Activate** — `ActivateGeneration` switches atomically
5. **Epoch** — optional registry `Advance` after activate
6. **Retire** — optional cleanup of the previous generation

Dry-run reserves and builds/verifies but aborts without activation. Snapshots expose generation metadata only (no values).

**See also:** `resource/cache/memory`, `resource/cache/redis`, `examples/developer-tool`.
