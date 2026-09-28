package log

import (
	"bytes"
	"errors"
	stdlog "log"
	"os"
	"strings"
	"testing"
)

func TestDebugPrintfHonorsEnabledState(t *testing.T) {
	var output bytes.Buffer
	previous := stdlog.Writer()
	stdlog.SetOutput(&output)
	t.Cleanup(func() {
		DisableDebug()
		stdlog.SetOutput(previous)
	})

	DisableDebug()
	DebugPrintf("hidden %d", 1)
	if output.Len() != 0 {
		t.Fatalf("disabled debug output = %q, want empty", output.String())
	}

	EnableDebug()
	DebugPrintf("visible %d", 2)
	if !strings.Contains(output.String(), "visible 2") {
		t.Fatalf("enabled debug output = %q, want visible message", output.String())
	}
}

func TestSetOutputRedirectsPackageWriters(t *testing.T) {
	previous := currentOutput()
	t.Cleanup(func() { SetOutput(previous) })

	var buffer bytes.Buffer
	SetOutput(&buffer)
	Println("redirected")
	if !strings.Contains(buffer.String(), "redirected") {
		t.Fatalf("redirected output = %q, want the message", buffer.String())
	}

	// NewLogger must follow the redirect too, otherwise hosts get a split
	// stream where structured loggers still write to the process stdout.
	var loggerBuffer bytes.Buffer
	SetOutput(&loggerBuffer)
	NewLogger("[TAG] ").Println("via logger")
	if !strings.Contains(loggerBuffer.String(), "via logger") {
		t.Fatalf("logger output = %q, want the message", loggerBuffer.String())
	}
}

func TestSetOutputRestoresStdoutForNil(t *testing.T) {
	previous := currentOutput()
	t.Cleanup(func() { SetOutput(previous) })

	SetOutput(nil)
	if currentOutput() != os.Stdout {
		t.Fatal("SetOutput(nil) did not restore os.Stdout")
	}
}

func TestFatalCallsHandlerInsteadOfExiting(t *testing.T) {
	t.Cleanup(func() { SetFatalHandler(nil) })

	var captured error
	SetFatalHandler(func(err error) { captured = err })

	Fatalf("boom %d", 42)
	if captured == nil {
		t.Fatal("Fatalf did not invoke the installed handler")
	}
	if !strings.Contains(captured.Error(), "boom 42") {
		t.Fatalf("handler error = %q, want formatted message", captured.Error())
	}

	captured = nil
	Fatal("plain failure")
	if captured == nil || !strings.Contains(captured.Error(), "plain failure") {
		t.Fatalf("Fatal handler error = %v, want the message", captured)
	}
}

func TestFatalHandlerResetRestoresExitingBehaviour(t *testing.T) {
	SetFatalHandler(func(error) {})
	SetFatalHandler(nil)

	if handleFatal(errors.New("x")) {
		t.Fatal("handleFatal reported a handler after the handler was cleared")
	}
}
