# Fencing

Fencing tokens increase monotonically. A stale generation must not release or mutate state owned by a newer token.

- `ErrTokenOverflow` on wrap — no silent wrap
- Validators reject tokens below the accepted high-water mark
- Leader election and supervisors must fence sensitive work with the current token
