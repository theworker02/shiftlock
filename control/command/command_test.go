package command_test

import (
	"context"
	"errors"
	"testing"

	"github.com/theworker02/shiftlock/control/command"
)

type denyAuth struct{}

func (denyAuth) AuthorizeCommand(actor, name, permission string) error { return errors.New("no") }

func TestPanicContainedAndDenied(t *testing.T) {
	r := command.New(command.Config{Authorizer: denyAuth{}})
	_ = r.Register(command.Spec{
		Name: "x", Permission: "command.invoke",
		Handler: func(ctx context.Context, req command.Request) (command.Result, error) {
			panic("nope")
		},
	})
	_, err := r.Invoke(context.Background(), command.Request{Name: "x", ActorID: "a"})
	if !errors.Is(err, command.ErrDenied) {
		t.Fatalf("got %v", err)
	}
}
