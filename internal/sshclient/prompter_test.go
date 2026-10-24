package sshclient

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func newTestPrompter(in string) (*Prompter, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return NewPrompter(out, strings.NewReader(in)), out
}

func TestAskEchoesInput(t *testing.T) {
	p, out := newTestPrompter("abc\r")
	got, err := p.Ask("Login: ")
	if err != nil || got != "abc" {
		t.Fatalf("got %q, %v", got, err)
	}
	if out.String() != "Login: abc\r\n" {
		t.Fatalf("out = %q", out.String())
	}
}

func TestAskSecretDoesNotEcho(t *testing.T) {
	p, out := newTestPrompter("pw\r")
	got, err := p.AskSecret("Password: ")
	if err != nil || got != "pw" {
		t.Fatalf("got %q, %v", got, err)
	}
	if strings.Contains(out.String(), "pw") {
		t.Fatalf("secret echoed: %q", out.String())
	}
	if out.String() != "Password: \r\n" {
		t.Fatalf("out = %q", out.String())
	}
}

func TestAskHandlesBackspace(t *testing.T) {
	p, out := newTestPrompter("abc\x7fd\n")
	got, err := p.Ask("> ")
	if err != nil || got != "abd" {
		t.Fatalf("got %q, %v", got, err)
	}
	if out.String() != "> abc\b \bd\r\n" {
		t.Fatalf("out = %q", out.String())
	}
}

func TestAskBackspaceOnEmptyDoesNothing(t *testing.T) {
	p, out := newTestPrompter("\b\bx\n")
	got, err := p.Ask("")
	if err != nil || got != "x" || out.String() != "x\r\n" {
		t.Fatalf("got %q out %q", got, out.String())
	}
}

func TestAskHandlesMultibyteBackspace(t *testing.T) {
	p, out := newTestPrompter("да\x7f\n")
	got, err := p.Ask("")
	if err != nil || got != "д" {
		t.Fatalf("got %q, %v", got, err)
	}
	if out.String() != "да\b \b\r\n" {
		t.Fatalf("out = %q", out.String())
	}
}

func TestConfirmRussianYes(t *testing.T) {
	p, _ := newTestPrompter("да\n")
	ok, err := p.Confirm("Верить?")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestConfirmAnswers(t *testing.T) {
	for in, want := range map[string]bool{
		"yes\n": true, " Y \n": true, "Д\n": true,
		"no\n": false, "N\r": false, "Нет\n": false, "н\n": false,
	} {
		p, _ := newTestPrompter(in)
		ok, err := p.Confirm("q")
		if err != nil || ok != want {
			t.Errorf("%q: ok=%v err=%v", in, ok, err)
		}
	}
}

func TestConfirmRepeatsOnGarbage(t *testing.T) {
	p, out := newTestPrompter("maybe\nn\n")
	ok, err := p.Confirm("Q?")
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if n := strings.Count(out.String(), "Q?"); n != 2 {
		t.Fatalf("prompt printed %d times: %q", n, out.String())
	}
	if !strings.Contains(out.String(), "Q? [yes/no] ") {
		t.Fatalf("no hint: %q", out.String())
	}
}

func TestCtrlCCancels(t *testing.T) {
	p, _ := newTestPrompter("ab\x03")
	if _, err := p.Ask("> "); !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v", err)
	}
	p, _ = newTestPrompter("\x03")
	if _, err := p.AskSecret("> "); !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v", err)
	}
}

func TestConfirmEOFCancels(t *testing.T) {
	p, _ := newTestPrompter("")
	if _, err := p.Confirm("Q?"); !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v", err)
	}
}

func TestAskSkipsEscapeSequences(t *testing.T) {
	p, out := newTestPrompter("ab\x1b[Dc\x1b[3~\x1bOA\r")
	got, err := p.Ask("")
	if err != nil || got != "abc" {
		t.Fatalf("got %q, %v", got, err)
	}
	if out.String() != "abc\r\n" {
		t.Fatalf("out = %q", out.String())
	}
}

func TestPrompterPending(t *testing.T) {
	p, _ := newTestPrompter("pw\rls\r")
	if got, err := p.AskSecret("Password: "); err != nil || got != "pw" {
		t.Fatalf("got %q, %v", got, err)
	}
	if got := string(p.Pending()); got != "ls\r" {
		t.Fatalf("Pending = %q", got)
	}
	if got := p.Pending(); len(got) != 0 {
		t.Fatalf("second Pending = %q", got)
	}
}

func TestPrompterPendingDropsLFAfterCR(t *testing.T) {
	p, _ := newTestPrompter("pw\r\nls")
	if _, err := p.AskSecret(""); err != nil {
		t.Fatal(err)
	}
	if got := string(p.Pending()); got != "ls" {
		t.Fatalf("Pending = %q", got)
	}
}

func TestPrompterPendingOnlySkippedLF(t *testing.T) {
	p, _ := newTestPrompter("pw\r\n")
	if _, err := p.AskSecret(""); err != nil {
		t.Fatal(err)
	}
	if got := p.Pending(); got != nil {
		t.Fatalf("Pending = %q, want nil", got)
	}
}
