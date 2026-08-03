# Pause a queue during maintenance

**Problem:** Consumers keep processing while a dependent database or API is unsafe.

**Approach:** Treat the queue as a resource with `Pause`/`Resume`. Drive pause/resume from a workflow step with operation IDs. Drift/reconcile controllers can re-assert the desired pause policy with bounded retry (paused during lockdown).

**See also:** `resource/queue`, `reconcile`, workflows.
