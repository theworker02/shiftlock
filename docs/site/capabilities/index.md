# Capabilities

Package `capability`: narrow, short-TTL, epoch-bound grants. Optional Ed25519 signatures.

Rules:

- Deny empty / wildcard (`*`) permissions at issue
- Delegation may only reduce scope
- Single-use / max-uses enforced
- Epoch advance invalidates prior tokens
- No secrets inside tokens
