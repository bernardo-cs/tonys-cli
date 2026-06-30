package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/bernardo-cs/tonys-cli/internal/toniecloud"
)

func TestErrorClassAndExitCodeMapping(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantClass string
		wantCode  int
	}{
		{"nil", nil, "error", 0},
		{"usage", usageErr("bad flag"), "usage", 2},
		{"not-found-local", notFoundErr("no tonie"), "not_found", 4},
		{"auth-sentinel", toniecloud.ErrNotAuthenticated, "auth", 3},
		{"api-401", &toniecloud.APIError{Status: 401}, "auth", 3},
		{"api-403", &toniecloud.APIError{Status: 403}, "auth", 3},
		{"api-404", &toniecloud.APIError{Status: 404}, "not_found", 4},
		{"api-500", &toniecloud.APIError{Status: 500}, "error", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.err != nil {
				if got := errorClass(c.err); got != c.wantClass {
					t.Errorf("errorClass = %q, want %q", got, c.wantClass)
				}
			}
			if got := ExitCode(c.err); got != c.wantCode {
				t.Errorf("ExitCode = %d, want %d", got, c.wantCode)
			}
		})
	}
}

func TestPrintErrorJSONContract(t *testing.T) {
	var buf bytes.Buffer
	a := &App{Output: "json", Stderr: &buf}
	a.PrintError(&toniecloud.APIError{Method: "GET", URL: "u", Status: 404, Detail: "nope"})

	var obj map[string]any
	if err := json.Unmarshal(buf.Bytes(), &obj); err != nil {
		t.Fatalf("error output is not valid JSON: %v (%q)", err, buf.String())
	}
	if obj["class"] != "not_found" {
		t.Errorf("class = %v, want not_found", obj["class"])
	}
	if obj["status"].(float64) != 404 {
		t.Errorf("status = %v, want 404", obj["status"])
	}
	if _, ok := obj["error"].(string); !ok {
		t.Errorf("missing error message: %v", obj)
	}
}

func TestPrintErrorHumanGoesToStderr(t *testing.T) {
	var out, errb bytes.Buffer
	a := &App{Output: "table", Stdout: &out, Stderr: &errb}
	a.PrintError(usageErr("boom"))
	if out.Len() != 0 {
		t.Errorf("stdout must stay clean, got %q", out.String())
	}
	if errb.Len() == 0 {
		t.Error("expected human error on stderr")
	}
}
