package server

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"
)

// PTY / SSH REPL regressions (reported 2025-03):
//   1) "Double Enter" — readLine must return immediately after CR when LF has not arrived yet.
//      A bufio.Peek(1) on an empty buffer blocks until the next byte (often the first char of the
//      following line), so one Enter could not submit the command.
//   2) "Duplicate claw> prompt" — a late LF after CR was read as an empty line on the next
//      readLine, causing an extra prompt before real input. Stale CR/LF must be discarded or
//      ignored when the next byte is clearly the start of a new command.

func TestReadLineWithEcho_CR(t *testing.T) {
	in := bytes.NewBufferString("hello\r")
	out := new(bytes.Buffer)
	br := bufio.NewReader(in)
	line, err := readLineWithEcho(br, out, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if line != "hello" {
		t.Fatalf("line=%q", line)
	}
	if out.String() != "hello" {
		t.Fatalf("echo=%q", out.String())
	}
}

func TestReadLineWithEcho_CRLF(t *testing.T) {
	in := bytes.NewBufferString("hi\r\n")
	out := new(bytes.Buffer)
	br := bufio.NewReader(in)
	line, err := readLineWithEcho(br, out, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if line != "hi" {
		t.Fatalf("line=%q", line)
	}
	if out.String() != "hi" {
		t.Fatalf("echo=%q", out.String())
	}
}

func TestReadLineWithEcho_LF(t *testing.T) {
	in := bytes.NewBufferString("x\n")
	out := new(bytes.Buffer)
	br := bufio.NewReader(in)
	line, err := readLineWithEcho(br, out, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if line != "x" {
		t.Fatalf("line=%q", line)
	}
}

func TestReadLineWithEcho_Backspace(t *testing.T) {
	// type "ab", backspace, "c", enter
	in := bytes.NewBuffer([]byte{'a', 'b', 0x7f, 'c', '\r'})
	out := new(bytes.Buffer)
	br := bufio.NewReader(in)
	line, err := readLineWithEcho(br, out, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if line != "ac" {
		t.Fatalf("line=%q", line)
	}
}

func TestReadLineWithEcho_UTF8Chinese(t *testing.T) {
	// "中文x", backspace removes x, then submit.
	in := bytes.NewBufferString("中文x\x7f\r")
	out := new(bytes.Buffer)
	br := bufio.NewReader(in)
	line, err := readLineWithEcho(br, out, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if line != "中文" {
		t.Fatalf("line=%q", line)
	}
	if out.String() == "" {
		t.Fatal("expected echoed UTF-8 bytes")
	}
}

// TestReadLineWithEcho_SplitCRLF ensures we do not block after CR waiting for LF across chunks.
func TestReadLineWithEcho_SplitCRLF(t *testing.T) {
	pr, pw := io.Pipe()
	br := bufio.NewReader(pr)
	out := io.Discard

	done1 := make(chan string, 1)
	go func() {
		line, err := readLineWithEcho(br, out, slog.Default())
		if err != nil {
			done1 <- "ERR:" + err.Error()
			return
		}
		done1 <- line
	}()

	if _, err := pw.Write([]byte("ok\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done1:
		if got != "ok" {
			t.Fatalf("first line: %q", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out: readLine blocked after CR (Peek on empty buffer bug)")
	}

	// Pipe Writes block until a reader consumes; run the second read in a goroutine first.
	done2 := make(chan string, 1)
	go func() {
		line, err := readLineWithEcho(br, out, slog.Default())
		if err != nil {
			done2 <- "ERR:" + err.Error()
			return
		}
		done2 <- line
	}()
	if _, err := pw.Write([]byte("\nx\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done2:
		if got != "x" {
			t.Fatalf("second line: %q (want \"x\" if stale LF was stripped)", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out second line")
	}
	_ = pw.Close()
}

// TestRegression_PTYPeekMustNotBlockAfterCarriageReturn guards against the "double Enter" bug:
// after '\r', we must not call Peek when bufio has no buffered byte (Peek would block on the
// next keystroke instead of finishing the line).
func TestRegression_PTYPeekMustNotBlockAfterCarriageReturn(t *testing.T) {
	pr, pw := io.Pipe()
	br := bufio.NewReader(pr)
	out := io.Discard

	done := make(chan string, 1)
	go func() {
		line, err := readLineWithEcho(br, out, slog.Default())
		if err != nil {
			done <- "ERR:" + err.Error()
			return
		}
		done <- line
	}()

	if _, err := pw.Write([]byte("check cpu\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got != "check cpu" {
			t.Fatalf("got %q, want full first line without waiting for another key", got)
		}
	case <-time.After(400 * time.Millisecond):
		t.Fatal("regression: readLine blocked after CR (old Peek-on-empty-buffer behavior)")
	}
	_ = pw.Close()
}

// TestRegression_PTYStaleLFBeforeNextCommand guards against the extra empty read that produced
// a second claw> before the user typed the next command.
func TestRegression_PTYStaleLFBeforeNextCommand(t *testing.T) {
	pr, pw := io.Pipe()
	br := bufio.NewReader(pr)
	out := io.Discard

	done1 := make(chan string, 1)
	go func() {
		line, err := readLineWithEcho(br, out, slog.Default())
		if err != nil {
			done1 <- "ERR:" + err.Error()
			return
		}
		done1 <- line
	}()
	if _, err := pw.Write([]byte("a\r")); err != nil {
		t.Fatal(err)
	}
	if got := <-done1; got != "a" {
		t.Fatalf("first line: %q", got)
	}

	done2 := make(chan string, 1)
	go func() {
		line, err := readLineWithEcho(br, out, slog.Default())
		if err != nil {
			done2 <- "ERR:" + err.Error()
			return
		}
		done2 <- line
	}()
	// Stale LF from CRLF split, then real second line (same pattern as interactive SSH).
	if _, err := pw.Write([]byte("\ncheck disk\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done2:
		if got != "check disk" {
			t.Fatalf("second line: %q — stale LF must not count as an empty line", got)
		}
	case <-time.After(400 * time.Millisecond):
		t.Fatal("timeout reading second line")
	}
	_ = pw.Close()
}

// TestRegression_replSequentialReads mimics two REPL iterations: CR ends line 1, LF arrives late,
// then line 2 is submitted with CR only.
func TestRegression_replSequentialReads(t *testing.T) {
	pr, pw := io.Pipe()
	br := bufio.NewReader(pr)
	out := new(bytes.Buffer)

	errCh := make(chan error, 1)
	line1Done := make(chan struct{})
	go func() {
		line1, err := readLineWithEcho(br, out, slog.Default())
		if err != nil {
			errCh <- err
			return
		}
		if line1 != "cmd1" {
			errCh <- fmt.Errorf("line1: got %q", line1)
			return
		}
		close(line1Done)

		line2, err := readLineWithEcho(br, out, slog.Default())
		if err != nil {
			errCh <- err
			return
		}
		if line2 != "cmd2" {
			errCh <- fmt.Errorf("line2: got %q", line2)
			return
		}
		errCh <- nil
	}()

	if _, err := pw.Write([]byte("cmd1\r")); err != nil {
		t.Fatal(err)
	}
	// Send stale LF + second line only after the first readLine has returned (deterministic).
	<-line1Done
	if _, err := pw.Write([]byte("\ncmd2\r")); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: two sequential readLineWithEcho calls (repl loop)")
	}

	if echo := out.String(); echo != "cmd1cmd2" {
		t.Fatalf("echo: got %q want cmd1cmd2", echo)
	}
	_ = pw.Close()
}
