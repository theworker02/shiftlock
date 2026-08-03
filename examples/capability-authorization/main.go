// Example capability-authorization issues, verifies, delegates, and rejects widening.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/theworker02/shiftlock/capability"
	"github.com/theworker02/shiftlock/security/signing"
)

func main() {
	key, err := signing.GenerateKey()
	if err != nil {
		fail(err)
	}
	ring := signing.NewKeyRing()
	_ = ring.Add(key.PublicView())
	auth := capability.New(capability.WithSigner(key, ring))

	parent, err := auth.Issue(capability.Request{
		Subject: "gen-1", Permission: "claim.release", Resource: "billing", TTL: time.Minute,
	})
	if err != nil {
		fail(err)
	}
	if err := auth.Verify(parent); err != nil {
		fail(err)
	}
	fmt.Printf("issued+verified id=%s\n", parent.ID)

	child, err := auth.Delegate(parent, capability.Request{
		Permission: "claim.release", Resource: "billing", TTL: 30 * time.Second,
	})
	if err != nil {
		fail(err)
	}
	fmt.Printf("delegated child id=%s parent=%s\n", child.ID, child.ParentID)

	if _, err := auth.Delegate(parent, capability.Request{Permission: "claim.revoke"}); err == nil {
		fail(fmt.Errorf("expected widen deny"))
	}
	fmt.Println("widen delegation denied")
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
