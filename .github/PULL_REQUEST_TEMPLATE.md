name: Pull Request
description: PR checklist for ShiftLock
body:
  - type: checkboxes
    id: checks
    attributes:
      label: Checklist
      options:
        - label: `go test ./...` passes
        - label: `go test -race ./...` passes
        - label: New backend changes pass contract suite
        - label: Safety-relevant changes include tests + docs
