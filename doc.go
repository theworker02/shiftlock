// Package shiftlock coordinates ownership handoff between process generations
// during restarts, rolling deployments, and infrastructure replacements.
//
// Only one generation may hold a valid committed fencing-token epoch for a
// given claim. Stale generations cannot overwrite or release newer ownership.
package shiftlock
