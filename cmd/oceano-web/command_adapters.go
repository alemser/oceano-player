package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type CommandRunner interface {
	Run(name string, args ...string) error
	OutputContext(ctx context.Context, name string, args ...string) ([]byte, error)
	CombinedOutput(name string, args ...string) ([]byte, error)
	CombinedOutputContext(ctx context.Context, name string, args ...string) ([]byte, error)
}

type OSCommandRunner struct{}

func (OSCommandRunner) Run(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

func (OSCommandRunner) OutputContext(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

func (OSCommandRunner) CombinedOutput(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

func (OSCommandRunner) CombinedOutputContext(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type ServiceManager interface {
	Restart(unit string) error
	PowerAction(action string)
	SignalMain(unit string, signal string) error
}

type SystemdServiceManager struct {
	runner CommandRunner
	sleep  func(time.Duration)
}

func NewSystemdServiceManager(runner CommandRunner) *SystemdServiceManager {
	return &SystemdServiceManager{
		runner: runner,
		sleep:  time.Sleep,
	}
}

func (m *SystemdServiceManager) Restart(unit string) error {
	if err := m.runner.Run("systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	out, err := m.runner.CombinedOutput("systemctl", "restart", unit)
	if err != nil {
		return fmt.Errorf("%s: %s", unit, strings.TrimSpace(string(out)))
	}
	m.sleep(500 * time.Millisecond)
	return nil
}

func (m *SystemdServiceManager) PowerAction(action string) {
	_ = m.runner.Run("systemctl", action)
}

func (m *SystemdServiceManager) SignalMain(unit string, signal string) error {
	return m.runner.Run("systemctl", "kill", "--kill-who=main", "--signal="+signal, unit)
}

var (
	commandRunner CommandRunner = OSCommandRunner{}
	serviceMgr    ServiceManager = NewSystemdServiceManager(commandRunner)
)
