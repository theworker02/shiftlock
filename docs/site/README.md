# ShiftLock Documentation Site

Markdown documentation hierarchy for Phase 6. Intended for MkDocs / similar static
generators later; until then browse these pages directly in the repository.

## Navigation

| Section | Path |
|---------|------|
| Introduction | [introduction/](introduction/) |
| Quick Start | [quick-start/](quick-start/) |
| Concepts | [concepts/](concepts/) |
| Ownership | [ownership/](ownership/) |
| Fencing | [fencing/](fencing/) |
| Handoffs | [handoffs/](handoffs/) |
| Supervisor | [supervisor/](supervisor/) |
| Leader Election | [leader-election/](leader-election/) |
| Scheduling | [scheduling/](scheduling/) |
| Barriers | [barriers/](barriers/) |
| Quorum | [quorum/](quorum/) |
| Capabilities | [capabilities/](capabilities/) |
| Security Policies | [security-policies/](security-policies/) |
| Maintenance | [maintenance/](maintenance/) |
| Lockdown | [lockdown/](lockdown/) |
| Configuration | [configuration/](configuration/) |
| Audit | [audit/](audit/) |
| Recovery | [recovery/](recovery/) |
| Backends | [backends/](backends/) |
| Integrations | [integrations/](integrations/) |
| Kubernetes | [kubernetes/](kubernetes/) |
| systemd | [systemd/](systemd/) |
| Windows | [windows/](windows/) |
| CLI | [cli/](cli/) |
| TUI | [tui/](tui/) |
| Operations | [operations/](operations/) |
| Threat Model | [threat-model/](threat-model/) |
| Security Hardening | [security-hardening/](security-hardening/) |
| Production Checklist | [production-checklist/](production-checklist/) |
| API Reference | [api-reference/](api-reference/) |
| Examples | [examples/](examples/) |

## Site goals

- Strong hierarchy and side navigation (not a card wall)
- Security warnings call out fail-closed defaults
- Backend capability tables and failure scenarios
- Copyable Go examples and CLI snippets
- Migration notes from Phase 5

See also:

- [Security model](../security-model.md)
- [Threat model](../threat-model.md)
- [Dependency discipline](../dependency-discipline.md)
- [Phase 6 audit](../audits/phase-6-audit.md)

## Future tooling

Planned: search, version selector, and branded MkDocs Material theme using
`assets/logo/shiftlock-horizontal.svg`. Content stubs are authoritative until
the generator is wired.
