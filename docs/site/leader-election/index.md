# Leader Election

Package `election` campaigns on claims + fencing tokens.

- Events channel is **bounded** (default 16; drops on overflow)
- `IsLeader()` is a local belief — fence work with `Token()`
- Resign / Close release leadership cleanly
