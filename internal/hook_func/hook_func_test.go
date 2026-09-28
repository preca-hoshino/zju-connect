package hook_func

import (
	"context"
	"errors"
	"testing"

	"github.com/mythologyli/zju-connect/configs"
)

func TestTerminalFuncsRunOnceInRegistrationOrder(t *testing.T) {
	t.Cleanup(Reset)

	var order []string
	RegisterTerminalFunc("first", func(context.Context) error {
		order = append(order, "first")
		return nil
	})
	RegisterTerminalFunc("second", func(context.Context) error {
		order = append(order, "second")
		return nil
	})

	if errs := ExecTerminalFunc(context.Background()); len(errs) != 0 {
		t.Fatalf("ExecTerminalFunc errors = %v, want none", errs)
	}
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("execution order = %v", order)
	}

	// A completed run must not execute the hooks a second time.
	order = nil
	if errs := ExecTerminalFunc(context.Background()); len(errs) != 0 {
		t.Fatalf("second ExecTerminalFunc errors = %v, want none", errs)
	}
	if len(order) != 0 {
		t.Fatalf("hooks ran again after the terminal flag was set: %v", order)
	}
}

func TestTerminalFuncErrorsAreCollectedAndReported(t *testing.T) {
	t.Cleanup(Reset)

	RegisterTerminalFunc("bad", func(context.Context) error {
		return errors.New("cleanup failed")
	})
	if errs := ExecTerminalFunc(context.Background()); len(errs) != 1 {
		t.Fatalf("ExecTerminalFunc errors = %v, want one failure", errs)
	}
}

// The CLI only ever runs one session, so it relies on registrations surviving
// for the life of the process. Embedding hosts reconnect, and without a reset
// the second session silently loses its cleanup hooks.
func TestResetAllowsRepeatedSessionsToRegisterHooks(t *testing.T) {
	t.Cleanup(Reset)

	RegisterTerminalFunc("session-one", func(context.Context) error { return nil })
	ExecTerminalFunc(context.Background())
	if !IsTerminal() {
		t.Fatal("IsTerminal() = false after a completed run")
	}

	Reset()
	if IsTerminal() {
		t.Fatal("Reset did not clear the terminal flag")
	}

	var secondRan bool
	RegisterTerminalFunc("session-two", func(context.Context) error {
		secondRan = true
		return nil
	})
	if errs := ExecTerminalFunc(context.Background()); len(errs) != 0 {
		t.Fatalf("second session errors = %v", errs)
	}
	if !secondRan {
		t.Fatal("second session hook did not run after Reset")
	}
}

func TestResetInitialClearsInitialFunctions(t *testing.T) {
	t.Cleanup(ResetInitial)

	ran := 0
	RegisterInitialFunc("init-one", func(context.Context, configs.Config) error {
		ran++
		return nil
	})
	ExecInitialFunc(context.Background(), configs.Config{})
	if ran != 1 {
		t.Fatalf("initial funcs ran %d times, want 1", ran)
	}

	ResetInitial()
	if IsInitial() {
		t.Fatal("ResetInitial did not clear the initial flag")
	}

	RegisterInitialFunc("init-two", func(context.Context, configs.Config) error {
		ran++
		return nil
	})
	ExecInitialFunc(context.Background(), configs.Config{})
	if ran != 2 {
		t.Fatalf("initial funcs ran %d times after reset, want 2", ran)
	}
}
