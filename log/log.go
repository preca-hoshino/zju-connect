package log

import (
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"sync/atomic"
)

var debug atomic.Bool

// output backs every writer exposed by this package. It defaults to os.Stdout
// so the CLI keeps its existing behaviour, but embedding hosts (the Flutter
// bindings, for instance) redirect it to a callback or discard it entirely.
var (
	outputMu sync.RWMutex
	output   io.Writer = os.Stdout
)

// fatalHandler, when set, takes over Fatal/Fatalf. See SetFatalHandler.
var (
	fatalMu      sync.RWMutex
	fatalHandler func(error)
)

// Init resets the log output to os.Stdout. It is kept for backwards
// compatibility with the CLI entry point.
func Init() {
	SetOutput(os.Stdout)
}

// SetOutput redirects every log write to w. Embedding hosts use this to route
// logs into their own logging system. Passing nil restores os.Stdout.
func SetOutput(w io.Writer) {
	if w == nil {
		w = os.Stdout
	}
	outputMu.Lock()
	output = w
	outputMu.Unlock()
	log.SetOutput(w)
}

func currentOutput() io.Writer {
	outputMu.RLock()
	defer outputMu.RUnlock()
	return output
}

// SetFatalHandler installs a handler that replaces the default "print and exit"
// behaviour of Fatal and Fatalf.
//
// This exists for embedding scenarios. A CLI process can afford to die on an
// unrecoverable error and let a supervisor restart it, but that is unacceptable
// for a library: os.Exit inside a Flutter app terminates the whole host process
// with no chance to surface an error to the user. Hosts therefore install a
// handler that records the error and unwinds normally.
//
// Passing nil restores the original fatal behaviour.
func SetFatalHandler(handler func(error)) {
	fatalMu.Lock()
	fatalHandler = handler
	fatalMu.Unlock()
}

func handleFatal(err error) bool {
	fatalMu.RLock()
	handler := fatalHandler
	fatalMu.RUnlock()
	if handler == nil {
		return false
	}
	handler(err)
	return true
}

// HasFatalHandler reports whether a fatal handler is installed, i.e. whether
// this process is running as an embedded library rather than a standalone CLI.
//
// Code that would otherwise terminate the host process (os.Exit in a background
// goroutine, for instance) uses this to decide between the two behaviours.
func HasFatalHandler() bool {
	fatalMu.RLock()
	defer fatalMu.RUnlock()
	return fatalHandler != nil
}

// Output returns the writer that currently receives log output.
func Output() io.Writer {
	return currentOutput()
}

func EnableDebug() {
	debug.Store(true)
}

func DisableDebug() {
	debug.Store(false)
}

func DebugEnabled() bool {
	return debug.Load()
}

func Print(v ...any) {
	log.Print(v...)
}

func DebugPrint(v ...any) {
	if debug.Load() {
		log.Print(v...)
	}
}

func Println(v ...any) {
	log.Println(v...)
}

func DebugPrintln(v ...any) {
	if debug.Load() {
		log.Println(v...)
	}
}

func Printf(format string, v ...any) {
	log.Printf(format, v...)
}

func DebugPrintf(format string, v ...any) {
	if debug.Load() {
		log.Printf(format, v...)
	}
}

// Fatal reports an unrecoverable error. When no fatal handler is installed it
// logs the message and terminates the process, matching os.Exit semantics.
// With a handler installed the call returns instead, which lets embedding
// hosts treat the failure as an error rather than losing their process.
func Fatal(v ...any) {
	if handleFatal(fmt.Errorf("%s", fmt.Sprint(v...))) {
		return
	}
	log.Fatal(v...)
}

// Fatalf is the formatted counterpart of Fatal.
func Fatalf(format string, v ...any) {
	if handleFatal(fmt.Errorf(format, v...)) {
		return
	}
	log.Fatalf(format, v...)
}

func DumpHex(buf []byte) {
	stdoutDumper := hex.Dumper(currentOutput())
	defer func(stdoutDumper io.WriteCloser) {
		_ = stdoutDumper.Close()
	}(stdoutDumper)
	_, _ = stdoutDumper.Write(buf)
}

func DebugDumpHex(buf []byte) {
	if debug.Load() {
		stdoutDumper := hex.Dumper(currentOutput())
		defer func(stdoutDumper io.WriteCloser) {
			_ = stdoutDumper.Close()
		}(stdoutDumper)
		_, _ = stdoutDumper.Write(buf)
	}
}

func NewLogger(prefix string) *log.Logger {
	return log.New(currentOutput(), prefix, log.LstdFlags)
}
