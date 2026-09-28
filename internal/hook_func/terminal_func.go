package hook_func

import (
	"context"
	"sync"

	"github.com/mythologyli/zju-connect/log"
)

type TerminalFunc func(ctx context.Context) error
type TerminalItem struct {
	f    TerminalFunc
	name string
}

var terminalFuncList []TerminalItem

var terminalBegin = false
var terminalMu sync.Mutex

func RegisterTerminalFunc(execName string, fun TerminalFunc) {
	terminalMu.Lock()
	defer terminalMu.Unlock()

	if terminalBegin {
		log.Println("Terminal already started, skip registering func:", execName)
		return
	}

	terminalFuncList = append(terminalFuncList, TerminalItem{
		f:    fun,
		name: execName,
	})
	log.Println("Register func on terminal:", execName)
}

func ExecTerminalFunc(ctx context.Context) []error {
	terminalMu.Lock()
	if terminalBegin {
		terminalMu.Unlock()
		return nil
	}
	terminalBegin = true
	funcList := append([]TerminalItem(nil), terminalFuncList...)
	terminalMu.Unlock()

	var errList []error
	for _, item := range funcList {
		log.Println("Exec func on terminal:", item.name)
		if err := item.f(ctx); err != nil {
			errList = append(errList, err)
			log.Println("Exec func on terminal ", item.name, "failed:", err)
		} else {
			log.Println("Exec func on terminal ", item.name, "success")
		}
	}
	return errList
}

func IsTerminal() bool {
	terminalMu.Lock()
	defer terminalMu.Unlock()

	return terminalBegin
}

// Reset clears the registered cleanup functions and the terminal flag.
//
// The CLI registers its hooks once and exits, so this is unnecessary there.
// An embedding host (the Flutter bindings, for example) may connect and
// disconnect repeatedly in one process; without a reset the second login
// would find terminalBegin already set and silently skip registering its own
// cleanup functions, leaking the previous session's state.
func Reset() {
	terminalMu.Lock()
	defer terminalMu.Unlock()

	terminalFuncList = nil
	terminalBegin = false
}

// ResetInitial clears the initial-function list.
//
// Like Reset it exists for hosts that run more than one session per process.
// The initial functions are invoked once, before the first connection, so a
// second run needs the list and the completion flag to start clean.
func ResetInitial() {
	initialFuncList = nil
	initialEnd = false
}
