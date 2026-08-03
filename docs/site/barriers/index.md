# Barriers

Package `barrier` coordinates multi-participant release with policies: minimum-count, exact, quorum, all.

`MaxParticipants` is a hard bound. Waiters are capped to the same bound — no unbounded waiter queues.
