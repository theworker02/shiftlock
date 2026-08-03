# ShiftLock Documentation Site

Published with **MkDocs Material** from this tree. Live site:
[https://theworker02.github.io/shiftlock/](https://theworker02.github.io/shiftlock/).

## Local preview

From the repository root (Python 3.10+):

```bash
pip install -r requirements-docs.txt
mkdocs serve -f docs/site/mkdocs.yml
```

Build:

```bash
mkdocs build --strict -f docs/site/mkdocs.yml
```

Site dependencies stay in `requirements-docs.txt` — never in `go.mod`.

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
| Problem guides | [problems/](problems/) |

## Deploy

GitHub Actions workflow [`.github/workflows/pages.yml`](../../.github/workflows/pages.yml)
builds on pushes to `main` that touch docs/site (and related paths) and on
`workflow_dispatch`.

**GitHub Pages source must be GitHub Actions** (Settings → Pages → Build and
deployment → Source: GitHub Actions).

## Branding

Theme colors follow [brand guidelines](../../assets/brand/brand-guidelines.md)
(Lock Cyan `#27C2D1`, Transfer Blue `#2D72E8`, Deep Navy `#0A1830`). Logos under
`assets/` are copies of `assets/logo/` for MkDocs `docs_dir` constraints.

See also:

- [Security model](https://github.com/theworker02/shiftlock/blob/main/docs/security-model.md)
- [Threat model](https://github.com/theworker02/shiftlock/blob/main/docs/threat-model.md)
- [Dependency discipline](https://github.com/theworker02/shiftlock/blob/main/docs/dependency-discipline.md)
