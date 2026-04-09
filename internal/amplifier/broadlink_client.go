package amplifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// BroadlinkClient abstracts sending a raw IR code to a Broadlink RM4 Mini.
// The code is a base64-encoded Broadlink IR packet captured via the learning
// workflow or sourced from a community database.
type BroadlinkClient interface {
	SendIRCode(code string) error
}

// MockBroadlinkClient implements BroadlinkClient for tests.
// It records every code sent so tests can assert which commands were issued.
type MockBroadlinkClient struct {
	Sent []string
	// Err, if non-nil, is returned by every SendIRCode call.
	Err error
}

func (m *MockBroadlinkClient) SendIRCode(code string) error {
	m.Sent = append(m.Sent, code)
	return m.Err
}

// NotImplementedBroadlinkClient is a production-safe placeholder used until
// the real Broadlink client is wired. It fails fast instead of pretending that
// commands succeeded.
type NotImplementedBroadlinkClient struct{}

func (c *NotImplementedBroadlinkClient) SendIRCode(_ string) error {
	return fmt.Errorf("broadlink IR sending is not implemented in this build")
}

// PythonBroadlinkClient sends IR codes via broadlink_bridge.py subprocess.
type PythonBroadlinkClient struct {
	BridgePath string
	Host       string
}

func (c *PythonBroadlinkClient) SendIRCode(code string) error {
	req, _ := json.Marshal(map[string]string{
		"cmd":  "send_ir",
		"host": c.Host,
		"code": code,
	})
	resp, err := runBridgeCommand(c.BridgePath, req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("broadlink bridge: %s", resp.Error)
	}
	return nil
}

// BridgePairResult holds the credentials returned by the bridge after pairing.
type BridgePairResult struct {
	Token    string
	DeviceID string
}

// BridgePair runs the pair command and returns device credentials.
func BridgePair(bridgePath, host string) (BridgePairResult, error) {
	req, _ := json.Marshal(map[string]string{
		"cmd":  "pair",
		"host": host,
	})
	resp, err := runBridgeCommand(bridgePath, req)
	if err != nil {
		return BridgePairResult{}, err
	}
	if !resp.OK {
		return BridgePairResult{}, fmt.Errorf("broadlink bridge: %s", resp.Error)
	}
	if resp.Token == "" || resp.DeviceID == "" {
		return BridgePairResult{}, fmt.Errorf("broadlink bridge: pair succeeded but returned empty credentials")
	}
	return BridgePairResult{Token: resp.Token, DeviceID: resp.DeviceID}, nil
}

// BridgeLearn puts the RM4 Mini into IR learning mode and waits up to timeoutSecs
// for the user to press a button on their remote. Returns the captured code as base64.
// This call blocks in the subprocess for the duration of the timeout.
func BridgeLearn(bridgePath, host string, timeoutSecs int) (string, error) {
	req, _ := json.Marshal(map[string]interface{}{
		"cmd":     "learn",
		"host":    host,
		"timeout": timeoutSecs,
	})
	resp, err := runBridgeCommand(bridgePath, req)
	if err != nil {
		return "", err
	}
	if !resp.OK {
		return "", fmt.Errorf("%s", resp.Error)
	}
	if resp.Code == "" {
		return "", fmt.Errorf("learn succeeded but returned empty code")
	}
	return resp.Code, nil
}

// bridgeResponse is the JSON envelope returned by broadlink_bridge.py.
type bridgeResponse struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Token    string `json:"token,omitempty"`
	DeviceID string `json:"device_id,omitempty"`
	Code     string `json:"code,omitempty"`
}

// venvPython is the Python interpreter inside the Oceano virtualenv.
const venvPython = "/opt/oceano-venv/bin/python3"

// findPython returns the venv interpreter if present, otherwise system python3.
func findPython() string {
	if _, err := os.Stat(venvPython); err == nil {
		return venvPython
	}
	return "python3"
}

// runBridgeCommand invokes broadlink_bridge.py, sends req on stdin, reads
// the single-line JSON response from stdout.
func runBridgeCommand(bridgePath string, req []byte) (bridgeResponse, error) {
	cmd := exec.Command(findPython(), bridgePath)
	cmd.Stdin = io.NopCloser(bytes.NewReader(append(req, '\n')))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	// Parse stdout first — bridge always reports errors as JSON there.
	line := strings.TrimSpace(stdout.String())
	if line != "" {
		var resp bridgeResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			return bridgeResponse{}, fmt.Errorf("broadlink bridge invalid response: %s", line)
		}
		return resp, nil
	}

	if runErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return bridgeResponse{}, fmt.Errorf("broadlink bridge crashed (%s):\n%s", runErr, detail)
		}
		return bridgeResponse{}, fmt.Errorf("broadlink bridge exited with %s and no output — run manually:\n  echo '{\"cmd\":\"pair\",\"host\":\"<ip>\"}' | python3 %s", runErr, bridgePath)
	}

	return bridgeResponse{}, fmt.Errorf("broadlink bridge returned no output")
}
