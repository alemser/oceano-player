package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type bluetoothTransportCommandRunnerStub struct {
	outputContextFn         func(ctx context.Context, name string, args ...string) ([]byte, error)
	combinedOutputContextFn func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func (s bluetoothTransportCommandRunnerStub) Run(name string, args ...string) error {
	return nil
}

func (s bluetoothTransportCommandRunnerStub) OutputContext(ctx context.Context, name string, args ...string) ([]byte, error) {
	if s.outputContextFn != nil {
		return s.outputContextFn(ctx, name, args...)
	}
	return nil, errors.New("not implemented")
}

func (s bluetoothTransportCommandRunnerStub) CombinedOutput(name string, args ...string) ([]byte, error) {
	return nil, nil
}

func (s bluetoothTransportCommandRunnerStub) CombinedOutputContext(ctx context.Context, name string, args ...string) ([]byte, error) {
	if s.combinedOutputContextFn != nil {
		return s.combinedOutputContextFn(ctx, name, args...)
	}
	return nil, errors.New("not implemented")
}

func TestBluezPlayerMethodForAction(t *testing.T) {
	tests := map[string]string{
		"play":  "Play",
		"pause": "Pause",
		"stop":  "Stop",
		"next":  "Next",
		"prev":  "Previous",
	}
	for action, want := range tests {
		got, ok := bluezPlayerMethodForAction(action)
		if !ok {
			t.Fatalf("action %q should be accepted", action)
		}
		if got != want {
			t.Fatalf("action %q => %q, want %q", action, got, want)
		}
	}
	if _, ok := bluezPlayerMethodForAction("invalid"); ok {
		t.Fatalf("invalid action should be rejected")
	}
}

func TestParseBluezPlayerPaths(t *testing.T) {
	raw := `
method return time=1.0 sender=:1.2 -> destination=:1.3 serial=14 reply_serial=4
   array [
      dict entry(
         object path "/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF/player0"
      )
      dict entry(
         object path "/org/bluez/hci0/dev_11_22_33_44_55_66/player0"
      )
      dict entry(
         object path "/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF/player0"
      )
   ]
`
	paths := parseBluezPlayerPaths(raw)
	if len(paths) != 2 {
		t.Fatalf("expected 2 unique paths, got %d", len(paths))
	}
	if paths[0] != "/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF/player0" {
		t.Fatalf("unexpected first path: %q", paths[0])
	}
}

func TestHandleBluetoothTransport_Success(t *testing.T) {
	origRunner := commandRunner
	t.Cleanup(func() { commandRunner = origRunner })

	commandRunner = bluetoothTransportCommandRunnerStub{
		outputContextFn: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return []byte(`object path "/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF/player0"`), nil
		},
		combinedOutputContextFn: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return []byte(""), nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/bluetooth/transport", strings.NewReader(`{"action":"next"}`))
	w := httptest.NewRecorder()

	handleBluetoothTransport()(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestHandleBluetoothTransport_NoPlayer(t *testing.T) {
	origRunner := commandRunner
	t.Cleanup(func() { commandRunner = origRunner })

	commandRunner = bluetoothTransportCommandRunnerStub{
		outputContextFn: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return []byte(`array []`), nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/bluetooth/transport", strings.NewReader(`{"action":"play"}`))
	w := httptest.NewRecorder()

	handleBluetoothTransport()(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleBluetoothTransportCapabilities_OK(t *testing.T) {
	origRunner := commandRunner
	t.Cleanup(func() { commandRunner = origRunner })

	commandRunner = bluetoothTransportCommandRunnerStub{
		outputContextFn: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return []byte(`object path "/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF/player0"`), nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/bluetooth/transport-capabilities", nil)
	w := httptest.NewRecorder()

	handleBluetoothTransportCapabilities()(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), `"available":true`) {
		t.Fatalf("expected available=true, got body=%s", w.Body.String())
	}
}

