# Fail over to a backup API provider

**Use:** failover group + health dimensions + resource epoch.

1. Detect unsafe primary (auth failure, sustained errors — not a single blip)
2. Acquire failover authority (capability / quorum as required)
3. Activate standby
4. Verify standby health
5. Advance resource epoch so stale callers are rejected
6. Journal the decision

Failback is a separate controlled workflow — never automatic by default.
