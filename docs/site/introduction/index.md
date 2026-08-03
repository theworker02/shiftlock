# Introduction

ShiftLock coordinates **ownership**, **fencing**, and (optionally) a **runtime control plane** for Go services.

It is not a hosted SaaS control plane, remote shell, or SIEM. Import it as a normal module:

```go
import "github.com/theworker02/shiftlock"
```

Core stays lightweight (stdlib). Advanced packages live under `capability/`, `guard/`, `audit/`, `control/`, `supervise/`, `election/`, `security/`.
