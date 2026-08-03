# Problem-oriented guides

ShiftLock is easiest to adopt by problem, not by package name.

| Problem | Approach |
|---------|----------|
| [Prevent two schedulers from running](prevent-dual-schedulers.md) | Claims + fencing / election |
| [Safely deploy a new worker generation](deploy-worker-generation.md) | Handoff + readiness + drain |
| [Coordinate a database migration](database-migration.md) | Resource lease + migration workflow |
| [Pause a queue during maintenance](queue-pause-maintenance.md) | Queue resource + maintenance mode |
| [Fail over to a backup API provider](api-provider-failover.md) | Failover group + resource epoch |
| [Rebuild a cache without partial activation](cache-generation.md) | Cache generation + epoch advance |
| [Protect an external API rate limit](api-rate-limit.md) | Rate-limit resource + budgets |
| [Recover an interrupted data migration](recover-migration.md) | Checkpoints + reconciliation |
| [Secure a production maintenance command](secure-maintenance-command.md) | Capabilities + guard + audit |
| [Coordinate multiple apps during a cutover](cross-service-cutover.md) | Workflows + barriers + service groups |

These pages are stubs that expand as Phase 7 adapters and workflows land.
