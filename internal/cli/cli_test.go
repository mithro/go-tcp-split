package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	Version = "1.2.post3"
	var out, errb bytes.Buffer
	if rc := Main([]string{"--version"}, &out, &errb); rc != 0 {
		t.Fatalf("rc=%d stderr=%q", rc, errb.String())
	}
	if got := out.String(); got != "tcp-split 1.2.post3\n" {
		t.Fatalf("stdout=%q", got)
	}
}

func TestUnknownFlagFails(t *testing.T) {
	var out, errb bytes.Buffer
	if rc := Main([]string{"--bogus"}, &out, &errb); rc == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(errb.String(), "bogus") {
		t.Fatalf("stderr=%q", errb.String())
	}
}
