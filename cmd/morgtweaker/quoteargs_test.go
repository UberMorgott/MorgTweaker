package main

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestQuoteArgsRoundTrip verifies the quoting is the true inverse of Windows'
// CommandLineToArgvW: build a command line from quoteArgs(args), parse it back
// with windows.DecomposeCommandLine, and require the parsed args to equal the
// originals. A dummy program token is prepended (the first token of a Windows
// command line is parsed by special rules) and dropped on decompose.
func TestQuoteArgsRoundTrip(t *testing.T) {
	cases := [][]string{
		{`plain`},
		{`has space`},
		{`tab	inside`},
		{`embedded"quote`},
		{`C:\Program Files\app`},
		{`C:\dir\`},                              // trailing backslash — the regression case
		{`C:\dir\\`},                             // trailing double backslash
		{`a\\\"b`},                               // backslashes before a quote
		{``},                                     // empty arg must survive as one empty arg
		{`one`, `C:\two with space\`, `three"q`}, // multiple args, mixed
		{`"already quoted"`},
		{`weird \ middle \ slashes`},
	}
	for _, args := range cases {
		cmd := "prog " + strings.Join(quoteArgs(args), " ")
		got, err := windows.DecomposeCommandLine(cmd)
		if err != nil {
			t.Fatalf("DecomposeCommandLine(%q): %v", cmd, err)
		}
		if len(got) == 0 || got[0] != "prog" {
			t.Fatalf("first token = %v, want prog (cmd=%q)", got, cmd)
		}
		got = got[1:]
		if len(got) != len(args) {
			t.Fatalf("arg count = %d (%v) want %d (%v) for cmd %q", len(got), got, len(args), args, cmd)
		}
		for i := range args {
			if got[i] != args[i] {
				t.Errorf("arg[%d] = %q want %q (cmd=%q, full=%v)", i, got[i], args[i], cmd, got)
			}
		}
	}
}

// TestQuoteArgTrailingBackslashEscaped is the focused regression test: a path
// ending in a backslash must NOT let its closing quote be escaped (which would
// merge it with the following argument).
func TestQuoteArgTrailingBackslashEscaped(t *testing.T) {
	// A space forces quoting; the trailing backslash run must then be doubled so it
	// does not escape the closing quote.
	const arg = `C:\my dir\`
	q := quoteArg(arg)
	if !strings.HasSuffix(q, `\\"`) {
		t.Errorf("quoteArg(%q) = %q; trailing run must be doubled before the closing quote", arg, q)
	}
	// And it must round-trip with a following arg kept separate (the regression: a
	// single `\"` here would merge `next` into this argument).
	got, err := windows.DecomposeCommandLine("prog " + q + " next")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[1] != arg || got[2] != "next" {
		t.Errorf("round-trip = %v, want [prog %q next]", got, arg)
	}
}

// TestQuoteArgNoQuotingWhenSafe: a token with no spaces/tabs/quotes is emitted
// verbatim (no spurious quoting).
func TestQuoteArgNoQuotingWhenSafe(t *testing.T) {
	if got := quoteArg(`/quiet`); got != `/quiet` {
		t.Errorf("quoteArg(/quiet) = %q, want verbatim", got)
	}
	if got := quoteArg(`C:\plain\path`); got != `C:\plain\path` {
		t.Errorf("quoteArg of a space-free path should be verbatim, got %q", got)
	}
}
