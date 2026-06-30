package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadData(t *testing.T) {
	a := NewApp()

	// literal JSON
	if got, err := readData(a, `{"a":1}`); err != nil || string(got) != `{"a":1}` {
		t.Fatalf("literal = %q, %v", got, err)
	}

	// stdin via "-"
	a.Stdin = strings.NewReader("from-stdin")
	if got, err := readData(a, "-"); err != nil || string(got) != "from-stdin" {
		t.Fatalf("stdin = %q, %v", got, err)
	}

	// @file
	dir := t.TempDir()
	p := filepath.Join(dir, "body.json")
	if err := os.WriteFile(p, []byte(`{"k":"v"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := readData(a, "@"+p); err != nil || string(got) != `{"k":"v"}` {
		t.Fatalf("@file = %q, %v", got, err)
	}
}

func TestPrintRaw(t *testing.T) {
	// json: pretty-printed and still valid JSON.
	var buf bytes.Buffer
	a := &App{Output: "json", Stdout: &buf}
	a.printRaw(json.RawMessage(`{"a":1,"b":2}`))
	if !json.Valid(bytes.TrimSpace(buf.Bytes())) {
		t.Fatalf("json output invalid: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "\n  ") {
		t.Errorf("expected indented JSON, got %q", buf.String())
	}

	// jsonl: single compact line.
	buf.Reset()
	a.Output = "jsonl"
	a.printRaw(json.RawMessage(`{"a":1}`))
	if got := buf.String(); got != "{\"a\":1}\n" {
		t.Errorf("jsonl = %q", got)
	}

	// non-JSON: echoed verbatim.
	buf.Reset()
	a.Output = "json"
	a.printRaw(json.RawMessage(`not json`))
	if got := strings.TrimSpace(buf.String()); got != "not json" {
		t.Errorf("passthrough = %q", got)
	}

	// empty: nothing written.
	buf.Reset()
	a.printRaw(json.RawMessage(`   `))
	if buf.Len() != 0 {
		t.Errorf("empty should write nothing, got %q", buf.String())
	}
}
