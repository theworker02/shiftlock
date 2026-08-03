# Fail over to a backup API provider

**Problem:** Primary payment (or other) HTTP provider is unhealthy.

**Approach:** Register primary + standby HTTP resources in a `failover` group. Manual policy requires an explicit target; health-based policy can *recommend* a standby. ExecuteFailover advances the resource epoch. Failback is a separate controlled call — never automatic.

**See also:** `failover`, `resource/service/http`.
