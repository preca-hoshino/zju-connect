package atrust

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mythologyli/zju-connect/client/atrust/auth"
	"github.com/mythologyli/zju-connect/log"
)

// recordingFatalHandler captures fatal reports so a test can assert that a
// library host is notified instead of losing the process.
type recordingFatalHandler struct {
	mu     sync.Mutex
	errs   []error
	notify chan error
}

func newRecordingFatalHandler() *recordingFatalHandler {
	return &recordingFatalHandler{notify: make(chan error, 8)}
}

func (h *recordingFatalHandler) handle(err error) {
	h.mu.Lock()
	h.errs = append(h.errs, err)
	h.mu.Unlock()
	select {
	case h.notify <- err:
	default:
	}
}

func (h *recordingFatalHandler) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-h.notify:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("fatal handler was not invoked")
		return nil
	}
}

func (h *recordingFatalHandler) first() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.errs) == 0 {
		return nil
	}
	return h.errs[0]
}

// installFatalHandler registers a handler for the duration of one test. It
// always restores the CLI behaviour so parallel tests keep exiting semantics.
func installFatalHandler(t *testing.T, handler *recordingFatalHandler) {
	t.Helper()
	log.SetFatalHandler(handler.handle)
	t.Cleanup(func() { log.SetFatalHandler(nil) })
}

func TestInvalidSessionIsReportedInsteadOfExiting(t *testing.T) {
	handler := newRecordingFatalHandler()
	installFatalHandler(t, handler)

	for _, tc := range []struct {
		name    string
		invoke  func() error
		wantMsg string
	}{
		{
			name: "ip tunnel",
			invoke: func() error {
				return parseIPAuthResponse([]byte(`{"code":10000004,"message":"session rejected"}`))
			},
			wantMsg: "IP tunnel: aTrust session is invalid (code 10000004): session rejected",
		},
		{
			name: "tcp tunnel",
			invoke: func() error {
				return parseTCPTunnelAuthResponse(`{"code":75500002,"message":"session rejected"}`)
			},
			wantMsg: "tcp tunnel: aTrust session is invalid (code 75500002): session rejected",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.invoke()
			if err == nil {
				t.Fatal("expected an error to be returned to the caller")
			}
			if !strings.Contains(err.Error(), "aTrust session is invalid") {
				t.Fatalf("returned error = %v, want session-invalid diagnostic", err)
			}
			reported := handler.wait(t)
			if !strings.Contains(reported.Error(), tc.wantMsg) {
				t.Fatalf("reported error = %q, want %q", reported.Error(), tc.wantMsg)
			}
		})
	}
}

func TestSessionRefreshFailureIsRecordedAndReported(t *testing.T) {
	handler := newRecordingFatalHandler()
	installFatalHandler(t, handler)

	c := NewClient(ClientOptions{Session: SessionOptions{SID: "old"}})
	defer c.Close()

	c.startSessionRefresh(func(context.Context) (auth.LoginResult, error) {
		return auth.LoginResult{}, fmt.Errorf("refresh rejected: %w", auth.ErrSessionInvalid)
	}, auth.ClientAuthData{}, nil, time.Millisecond)

	reported := handler.wait(t)
	if !strings.Contains(reported.Error(), "aTrust session maintenance failed") {
		t.Fatalf("reported error = %q, want the maintenance failure", reported.Error())
	}

	// The refresh loop must stop after an invalid session rather than spinning.
	select {
	case <-c.refreshDone:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh loop did not stop after an invalid session")
	}

	// Subsequent operations must fail fast on the recorded error instead of
	// silently continuing with a dead SID.
	sid, err := c.sessionSID()
	if err == nil || sid != "" {
		t.Fatalf("sessionSID = %q, %v; want the recorded invalid-session error", sid, err)
	}
}

func TestLK3TunnelSurfacesSessionInvalidThroughRead(t *testing.T) {
	handler := newRecordingFatalHandler()
	installFatalHandler(t, handler)

	// Only the session-invalid branch is exercised here, so the connection does
	// not need a live tunnel: handleAuthResp returns before touching conntrack.
	conn := &l3TunnelConn{}
	err := conn.handleAuthResp(0, []byte(`{"code":10000004,"message":"session rejected"}`))
	if err == nil {
		t.Fatal("handleAuthResp did not surface the session-invalid error")
	}
	if !strings.Contains(err.Error(), "l3-tunnel resource auth: aTrust session is invalid") {
		t.Fatalf("handleAuthResp error = %v, want the resource auth diagnostic", err)
	}
	if reported := handler.wait(t); !strings.Contains(reported.Error(), "l3-tunnel resource auth") {
		t.Fatalf("reported error = %q, want the resource auth diagnostic", reported.Error())
	}

	// readLoop turns that returned error into a recorded terminal state, which
	// forwardFromConn inspects to decide against reconnecting.
	conn.reportSessionInvalid(err)
	if !errors.Is(conn.sessionInvalidError(), err) {
		t.Fatalf("l3TunnelConn did not record the session-invalid error")
	}
}

func TestL3TunnelReportsSessionInvalidThroughConn(t *testing.T) {
	handler := newRecordingFatalHandler()
	installFatalHandler(t, handler)

	// Build a tunnel without a client: only the session-error plumbing is under
	// test, and NewL3Tunnel's client-dependent setup is skipped by leaving it nil
	// via the zero-value tunnel plus explicit channel initialisation.
	tunnel := &L3Tunnel{
		dataChan: make(chan []byte, 4),
		closeCh:  make(chan struct{}),
	}

	want := errors.New("aTrust session is invalid (code 10000004): rejected")
	tunnel.reportSessionInvalid(want)
	if !errors.Is(tunnel.SessionInvalidError(), want) {
		t.Fatalf("SessionInvalidError = %v, want the recorded error", tunnel.SessionInvalidError())
	}

	conn := &L3Conn{l3Tunnel: tunnel, closeCh: make(chan struct{})}

	// Read must report the session error rather than a bare io.EOF, otherwise
	// the caller treats it as an ordinary disconnect and retries.
	if _, err := conn.Read(make([]byte, 16)); !errors.Is(err, want) {
		t.Fatalf("Read error = %v, want the recorded session error", err)
	}
	if _, err := conn.Write([]byte{0x45}); !errors.Is(err, want) {
		t.Fatalf("Write error = %v, want the recorded session error", err)
	}
}
