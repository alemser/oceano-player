package amplifier

import "fmt"

// BroadlinkClient abstracts sending a raw IR code to a Broadlink RM4 Mini.
// The code is a base64-encoded Broadlink IR packet captured via the learning
// workflow or sourced from a community database.
//
// The real implementation (subprocess bridge via python-broadlink) is added in
// Milestone 5 when the physical device is available. Until then, MockBroadlinkClient
// is used for all unit and integration tests.
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
