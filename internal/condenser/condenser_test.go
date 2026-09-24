package condenser

import (
	"strings"
	"testing"
)

func TestTypeScriptCondenser(t *testing.T) {
	raw := `
src/app.tsx:10:5 - error TS2339: Property 'user' does not exist on type 'Session'.
    const u = session.user;
              ~~~~
src/components/Header.tsx:25:12 - error TS2339: Property 'user' does not exist on type 'Session'.
    return <div>{session.user.name}</div>;
                         ~~~~
src/routes/api.tsx:50:8 - error TS2339: Property 'user' does not exist on type 'Session'.
    send(session.user);
                 ~~~~
src/routes/auth.tsx:12:8 - error TS2339: Property 'user' does not exist on type 'Session'.
    check(session.user);
                  ~~~~
src/routes/profile.tsx:19:8 - error TS2339: Property 'user' does not exist on type 'Session'.
    render(session.user);
                   ~~~~
src/routes/settings.tsx:44:8 - error TS2339: Property 'user' does not exist on type 'Session'.
    save(session.user);
                 ~~~~
src/models/types.ts:80:1 - error TS2322: Type 'string' is not assignable to type 'number'.
    const x: number = "hello";
          ~
`
	res, err := Condense(raw, CondenseOptions{
		Format:      FormatTypeScript,
		ShowSavings: true,
	})
	if err != nil {
		t.Fatalf("Condense failed: %v", err)
	}

	if !strings.Contains(res.Condensed, "[TS2339] (Occurred 6 times)") {
		t.Errorf("expected TS2339 group header, got:\n%s", res.Condensed)
	}
	if !strings.Contains(res.Condensed, "[TS2322] (Occurred 1 times)") {
		t.Errorf("expected TS2322 group header, got:\n%s", res.Condensed)
	}
	if !strings.Contains(res.Condensed, "Token Diet") {
		t.Errorf("expected Token Diet footer, got:\n%s", res.Condensed)
	}
}

func TestGoPanicCondenser(t *testing.T) {
	raw := `
panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: code=0x2 addr=0x0 pc=0x104b2a8d4]

goroutine 1 [running]:
github.com/VinnyVanGogh/staypoint/internal/auth.Validate(0x0)
	/Users/vincevasile/dev/agent-mesh/internal/auth/auth.go:42 +0x24
runtime.panicmem(...)
	/usr/local/go/src/runtime/panic.go:260
runtime.sigpanic()
	/usr/local/go/src/runtime/signal_unix.go:881
main.main()
	/Users/vincevasile/dev/agent-mesh/cmd/mesh/main.go:12 +0x30

goroutine 2 [chan receive]:
runtime.gopark(0x104d8c890, 0x1400010c0b8, 0xb, 0x17)
	/usr/local/go/src/runtime/proc.go:420
runtime.chanrecv(0x1400010c060, 0x0, 0x1)
	/usr/local/go/src/runtime/chan.go:640

goroutine 3 [select]:
runtime.gopark(0x104d8c890, 0x1400010c0b8, 0xb, 0x17)
	/usr/local/go/src/runtime/proc.go:420
`
	res, err := Condense(raw, CondenseOptions{
		Format:      FormatGo,
		ShowSavings: true,
	})
	if err != nil {
		t.Fatalf("Condense failed: %v", err)
	}

	if !strings.Contains(res.Condensed, "panic: runtime error:") {
		t.Errorf("expected panic message, got:\n%s", res.Condensed)
	}
	if !strings.Contains(res.Condensed, "auth.go:42") {
		t.Errorf("expected application frame auth.go:42, got:\n%s", res.Condensed)
	}
	if strings.Contains(res.Condensed, "goroutine 2 [chan receive]") {
		t.Errorf("expected idle goroutine 2 to be stripped, got:\n%s", res.Condensed)
	}
	if strings.Contains(res.Condensed, "goroutine 3 [select]") {
		t.Errorf("expected idle goroutine 3 to be stripped, got:\n%s", res.Condensed)
	}
}

func TestPythonTracebackCondenser(t *testing.T) {
	raw := `
Traceback (most recent call last):
  File "main.py", line 15, in <module>
    app.run()
  File "/Users/vincevasile/.venv/lib/python3.11/site-packages/flask/app.py", line 880, in run
    return wsgi.server(self)
  File "/Users/vincevasile/.venv/lib/python3.11/site-packages/werkzeug/serving.py", line 1024, in run_simple
    s.bind(server_address)
  File "/Users/vincevasile/.venv/lib/python3.11/site-packages/werkzeug/serving.py", line 240, in bind
    super().bind(value)
  File "src/routes.py", line 32, in handle_request
    raise ValueError("Invalid user payload")
ValueError: Invalid user payload
`
	res, err := Condense(raw, CondenseOptions{
		Format:      FormatPython,
		ShowSavings: true,
	})
	if err != nil {
		t.Fatalf("Condense failed: %v", err)
	}

	if !strings.Contains(res.Condensed, "Traceback (most recent call last):") {
		t.Errorf("expected traceback header, got:\n%s", res.Condensed)
	}
	if !strings.Contains(res.Condensed, "ValueError: Invalid user payload") {
		t.Errorf("expected exception message, got:\n%s", res.Condensed)
	}
	if !strings.Contains(res.Condensed, "collapsed") {
		t.Errorf("expected collapsed site-packages frames, got:\n%s", res.Condensed)
	}
	if !strings.Contains(res.Condensed, "src/routes.py") {
		t.Errorf("expected user frame src/routes.py to be kept, got:\n%s", res.Condensed)
	}
}

func TestGenericCondenser(t *testing.T) {
	raw := "\x1b[31mError:\x1b[0m connecting to socket\nconnecting to socket\nconnecting to socket\nconnecting to socket\nDone."
	res, err := Condense(raw, CondenseOptions{
		Format:      FormatGeneric,
		ShowSavings: false,
	})
	if err != nil {
		t.Fatalf("Condense failed: %v", err)
	}

	if strings.Contains(res.Condensed, "\x1b[") {
		t.Errorf("expected ANSI codes to be stripped, got:\n%s", res.Condensed)
	}
	if !strings.Contains(res.Condensed, "repeated") {
		t.Errorf("expected repeated lines to be deduplicated, got:\n%s", res.Condensed)
	}
}
