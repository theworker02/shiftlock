# Prevent two schedulers from running

**Problem:** Two process instances both believe they own a nightly job.

**Approach:** Register a `scheduler` or `worker` resource and acquire an **exclusive** lease (or use classic ShiftLock claim ownership). Combine with fencing when the adapter advertises `SupportsFencing`.

**See also:** Resource leases (`resource.LeaseManager`), ownership claims in the root module.
