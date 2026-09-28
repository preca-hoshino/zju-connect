package hook_func

import (
	"context"
	"fmt"
	"net"

	"github.com/mythologyli/zju-connect/configs"
	"github.com/mythologyli/zju-connect/log"
)

type InitialFunc func(ctx context.Context, config configs.Config) error
type InitialItem struct {
	f    InitialFunc
	name string
}

var initialFuncList []InitialItem

var initialEnd = false

func RegisterInitialFunc(execName string, fun InitialFunc) {
	initialFuncList = append(initialFuncList, InitialItem{
		f:    fun,
		name: execName,
	})
}

func ExecInitialFunc(ctx context.Context, config configs.Config) []error {
	var errList []error
	for _, item := range initialFuncList {
		log.Println("Exec func on initial:", item.name)
		if err := item.f(ctx, config); err != nil {
			errList = append(errList, err)
			log.Println("Exec func on initial ", item.name, "failed:", err)
		} else {
			log.Println("Exec func on initial ", item.name, "success")
		}
	}
	initialEnd = true
	return errList
}

func IsInitial() bool {
	return initialEnd
}

// checkBindPortLegal validates that the configured listen addresses parse and
// carry a non-zero port.
//
// It deliberately does not inspect the live socket table: that used to rely on
// gopsutil, which drags in `purego` and therefore a cgo requirement on iOS.
// This package is compiled into the Flutter bindings, so the dependency had to
// go. Binding the port afterwards still surfaces a real conflict as a listen
// error; this only loses the earlier, friendlier message.
func checkBindPortLegal(ctx context.Context, config configs.Config) error {
	_ = ctx

	for _, addrStr := range []string{config.HTTPBind, config.SocksBind} {
		if len(addrStr) == 0 {
			continue
		}
		addr, err := net.ResolveTCPAddr("tcp", addrStr)
		if err != nil || addr.Port == 0 {
			return fmt.Errorf("the value %q for the listen address is incorrect. Please refer to the README for the correct format", addrStr)
		}
	}

	for _, addrStr := range []string{config.DNSServerBind} {
		if len(addrStr) == 0 {
			continue
		}
		addr, err := net.ResolveUDPAddr("udp", addrStr)
		if err != nil || addr.Port == 0 {
			return fmt.Errorf("the value %q for the DNS listen address is incorrect. Please refer to the README for the correct format", addrStr)
		}
	}

	return nil
}
