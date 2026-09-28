// Package main exposes the zju-connect binding as a C shared library.
//
// Build it with:
//
//	go build -buildmode=c-shared -o libzju_connect.so ./binding/capi
//
// The generated header (libzju_connect.h) is the ABI used by the Dart FFI
// bindings. A hand-written copy of the same declarations lives in
// binding/capi/include/zju_connect.h for ffigen.
//
// Design notes:
//
//   - The session handle is an integer (uintptr) rather than a pointer, which
//     keeps the ABI simple and avoids passing Go pointers to C.
//   - Host callbacks are plain C function pointers received once at creation.
//     cgo cannot call a C function pointer directly, so the preamble provides
//     static trampolines.
//   - All returned strings are heap allocated with C.CString and must be freed
//     with zcFreeString.
//
// It is a main package because Go requires exactly one main package for
// -buildmode=c-shared. main itself is never executed by hosts that link the
// library, so it stays empty.
package main

/*
#include <stdint.h>
#include <stdlib.h>

// zc_client is an opaque session handle. Zero is never a valid handle.
typedef uintptr_t zc_client;

// zc_event_fn receives asynchronous events (logs, state changes, errors).
// type: 0=log, 1=state, 2=error.
typedef void (*zc_event_fn)(void* user_data, int type, const char* message);

// zc_challenge_fn receives an authentication challenge. The host must answer
// it with zcRespondChallenge using the same challenge_id.
typedef void (*zc_challenge_fn)(void* user_data, long long challenge_id, const char* payload);

// Static trampolines: cgo cannot invoke a C function pointer directly.
static void zc_emit_event(zc_event_fn fn, void* user_data, int type, const char* message) {
	if (fn != NULL) {
		fn(user_data, type, message);
	}
}

static void zc_emit_challenge(zc_challenge_fn fn, void* user_data, long long challenge_id, const char* payload) {
	if (fn != NULL) {
		fn(user_data, challenge_id, payload);
	}
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"time"
	"unsafe"

	"github.com/mythologyli/zju-connect/binding"

	"runtime/cgo"
)

// Error codes returned by functions that report a negative result.
const (
	zcOK          = 0
	zcErrInvalid  = -1
	zcErrTimeout  = -2
	zcErrClosed   = -3
	zcErrInternal = -4
)

func handleOf(h C.zc_client) (cgo.Handle, bool) {
	if h == 0 {
		return 0, false
	}
	return cgo.Handle(h), true
}

func storeErr(errOut **C.char, err error) {
	if errOut == nil {
		return
	}
	if err == nil {
		*errOut = nil
		return
	}
	*errOut = C.CString(err.Error())
}

// zcEventSink adapts the host's C callbacks to Go function values.
type zcEventSink struct {
	userData unsafe.Pointer
	onEvent  C.zc_event_fn
	onChal   C.zc_challenge_fn
}

func (s *zcEventSink) event(event binding.Event) {
	cMessage := C.CString(event.Message)
	defer C.free(unsafe.Pointer(cMessage))
	C.zc_emit_event(s.onEvent, s.userData, C.int(event.Type), cMessage)
}

func (s *zcEventSink) challenge(challenge binding.Challenge) {
	cPayload := C.CString(challenge.Payload)
	defer C.free(unsafe.Pointer(cPayload))
	C.zc_emit_challenge(s.onChal, s.userData, C.longlong(challenge.ID), cPayload)
}

// zcNew creates a session. configJSON is a JSON object matching binding.Config.
//
// The callbacks are optional. user_data is passed back verbatim; it is not
// dereferenced by this library.
//
// On failure it returns 0 and, when errOut is non-NULL, stores a message that
// the caller must free with zcFreeString.
//
//export zcNew
func zcNew(configJSON *C.char, userData unsafe.Pointer, onEvent C.zc_event_fn, onChallenge C.zc_challenge_fn, errOut **C.char) C.zc_client {
	if configJSON == nil {
		storeErr(errOut, fmt.Errorf("config JSON is NULL"))
		return 0
	}

	cfg, err := binding.ParseConfig([]byte(C.GoString(configJSON)))
	if err != nil {
		storeErr(errOut, err)
		return 0
	}

	sink := &zcEventSink{userData: userData, onEvent: onEvent, onChal: onChallenge}

	client, err := binding.NewClient(cfg, sink.event, sink.challenge)
	if err != nil {
		storeErr(errOut, err)
		return 0
	}

	handle := cgo.NewHandle(client)
	storeErr(errOut, nil)
	return C.zc_client(handle)
}

// zcSetup performs the VPN login. It returns 0 on success and a negative code
// on failure; the error message is delivered through the event callback.
//
// zcSetup blocks until the login finishes and may take a while. Call it from a
// background thread.
//
//export zcSetup
func zcSetup(handle C.zc_client) C.int {
	h, ok := handleOf(handle)
	if !ok {
		return zcErrInvalid
	}
	client, ok := h.Value().(*binding.Client)
	if !ok {
		return zcErrInvalid
	}
	if err := client.Setup(); err != nil {
		return zcErrInternal
	}
	return zcOK
}

// zcClose tears the session down. The handle stays valid for zcStatJSON until
// zcFree is called.
//
//export zcClose
func zcClose(handle C.zc_client) {
	h, ok := handleOf(handle)
	if !ok {
		return
	}
	if client, ok := h.Value().(*binding.Client); ok {
		client.Close()
	}
}

// zcFree releases the handle. After this call the handle must not be used
// again. It does not close the session; call zcClose first.
//
//export zcFree
func zcFree(handle C.zc_client) {
	h, ok := handleOf(handle)
	if !ok {
		return
	}
	h.Delete()
}

// zcStatJSON returns a JSON object describing the session:
//
//	{"state":"connected","virtual_ip":"10.0.0.1","session_id":"...","client_data":"..."}
//
// The caller owns the string and must free it with zcFreeString.
//
//export zcStatJSON
func zcStatJSON(handle C.zc_client) *C.char {
	h, ok := handleOf(handle)
	if !ok {
		return nil
	}
	client, ok := h.Value().(*binding.Client)
	if !ok {
		return nil
	}
	stat := map[string]string{
		"state":       client.State(),
		"virtual_ip":  client.VirtualIP(),
		"session_id":  client.SessionID(),
		"client_data": client.ClientData(),
	}
	encoded, err := json.Marshal(stat)
	if err != nil {
		return nil
	}
	return C.CString(string(encoded))
}

// zcStartProxy starts the local SOCKS5 and HTTP proxies. proxyConfigJSON is a
// JSON object matching binding.ProxyConfig.
//
// It returns a JSON object {"socks":"127.0.0.1:1234","http":""} that the caller
// must free with zcFreeString, or NULL on failure.
//
//export zcStartProxy
func zcStartProxy(handle C.zc_client, proxyConfigJSON *C.char) *C.char {
	h, ok := handleOf(handle)
	if !ok {
		return nil
	}
	client, ok := h.Value().(*binding.Client)
	if !ok {
		return nil
	}

	var cfg binding.ProxyConfig
	if proxyConfigJSON != nil {
		if err := json.Unmarshal([]byte(C.GoString(proxyConfigJSON)), &cfg); err != nil {
			return nil
		}
	}

	addrs, err := client.StartProxy(cfg)
	if err != nil {
		return nil
	}
	encoded, err := json.Marshal(addrs)
	if err != nil {
		return nil
	}
	return C.CString(string(encoded))
}

// zcReadPacket reads one IPv4 packet from the tunnel into buf.
//
// It returns the number of bytes read, or a negative code:
//
//	zcErrTimeout (-2) no packet arrived within timeout_ms
//	zcErrClosed  (-3) the session was closed
//	any other negative value is an error
//
// A timeout_ms of zero blocks indefinitely. The call blocks, so invoke it from
// a dedicated thread.
//
//export zcReadPacket
func zcReadPacket(handle C.zc_client, buf *C.uint8_t, length C.int, timeoutMS C.int) C.int {
	h, ok := handleOf(handle)
	if !ok {
		return zcErrInvalid
	}
	client, ok := h.Value().(*binding.Client)
	if !ok {
		return zcErrInvalid
	}
	if buf == nil || length <= 0 {
		return zcErrInvalid
	}

	slice := unsafe.Slice((*byte)(unsafe.Pointer(buf)), int(length))
	timeout := time.Duration(timeoutMS) * time.Millisecond

	n, err := client.ReadPacket(slice, timeout)
	switch {
	case err == nil:
		return C.int(n)
	case err == binding.ErrTimeout:
		return zcErrTimeout
	case err == binding.ErrClosed:
		return zcErrClosed
	default:
		return zcErrInternal
	}
}

// zcWritePacket sends one IPv4 packet to the tunnel. It returns 0 on success
// and a negative code on failure.
//
//export zcWritePacket
func zcWritePacket(handle C.zc_client, buf *C.uint8_t, length C.int) C.int {
	h, ok := handleOf(handle)
	if !ok {
		return zcErrInvalid
	}
	client, ok := h.Value().(*binding.Client)
	if !ok {
		return zcErrInvalid
	}
	if buf == nil || length <= 0 {
		return zcErrInvalid
	}

	packet := unsafe.Slice((*byte)(unsafe.Pointer(buf)), int(length))
	if err := client.WritePacket(packet); err != nil {
		if err == binding.ErrClosed {
			return zcErrClosed
		}
		return zcErrInternal
	}
	return zcOK
}

// zcRespondChallenge answers a challenge previously delivered to
// zc_challenge_fn. responseJSON is a JSON object whose schema depends on the
// challenge kind.
//
// It returns 0 on success and a negative code if the challenge id is unknown.
//
//export zcRespondChallenge
func zcRespondChallenge(handle C.zc_client, challengeID C.longlong, responseJSON *C.char) C.int {
	h, ok := handleOf(handle)
	if !ok {
		return zcErrInvalid
	}
	client, ok := h.Value().(*binding.Client)
	if !ok {
		return zcErrInvalid
	}

	response := ""
	if responseJSON != nil {
		response = C.GoString(responseJSON)
	}
	if !client.RespondChallenge(int64(challengeID), response) {
		return zcErrInvalid
	}
	return zcOK
}

// zcFreeString frees a string previously returned by this library.
//
//export zcFreeString
func zcFreeString(str *C.char) {
	if str == nil {
		return
	}
	C.free(unsafe.Pointer(str))
}

// main is required by -buildmode=c-shared. It is never called when the library
// is loaded by a host process.
func main() {}
