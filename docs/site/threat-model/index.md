# Threat Model (site)

Canonical document: [docs/threat-model.md](../../threat-model.md).

Trust the backend store and deploying operator. Library callers are not cryptographically authenticated unless you bind principals via capabilities / mTLS at the application edge.
