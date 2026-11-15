package transcoder

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// RunOptions specifies custom configuration when executing a command.
type RunOptions struct {
	Name   string
	Args   []string
	Dir    string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// CommandRunner abstracts subprocess execution for testing and production.
type CommandRunner interface {
	// Run executes a command and returns its standard output bytes.
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
	// RunCustom executes a command with custom options (such as custom stdout/stderr writers).
	RunCustom(ctx context.Context, opt RunOptions) error
}

// RealRunner executes external commands on the host OS via exec.CommandContext.
type RealRunner struct{}

// NewRealRunner creates a new RealRunner instance.
func NewRealRunner() *RealRunner {
	return &RealRunner{}
}

// Run executes a command using os/exec and returns its standard output.
func (r *RealRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if stderr.Len() > 0 {
			return stdout.Bytes(), fmt.Errorf("%s failed: %w (stderr: %s)", name, err, stderr.String())
		}
		return stdout.Bytes(), fmt.Errorf("%s failed: %w", name, err)
	}
	return stdout.Bytes(), nil
}

// RunCustom executes a command with specified options using os/exec.
func (r *RealRunner) RunCustom(ctx context.Context, opt RunOptions) error {
	cmd := exec.CommandContext(ctx, opt.Name, opt.Args...)
	if opt.Dir != "" {
		cmd.Dir = opt.Dir
	}
	if opt.Stdin != nil {
		cmd.Stdin = opt.Stdin
	}
	if opt.Stdout != nil {
		cmd.Stdout = opt.Stdout
	}
	if opt.Stderr != nil {
		cmd.Stderr = opt.Stderr
	}
	return cmd.Run()
}

// MockHandler defines a custom callback for MockRunner executions.
type MockHandler func(ctx context.Context, opt RunOptions) error

// MockRunner records calls and executes registered mock handlers for 100% offline testing.
type MockRunner struct {
	mu             sync.Mutex
	calls          []RunOptions
	handlers       map[string]MockHandler
	defaultHandler MockHandler
}

// NewMockRunner returns an initialized MockRunner.
func NewMockRunner() *MockRunner {
	return &MockRunner{
		handlers: make(map[string]MockHandler),
	}
}

// RegisterHandler registers a command handler for a specific command name (e.g. "ffprobe", "ffmpeg").
func (m *MockRunner) RegisterHandler(name string, handler MockHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers[name] = handler
}

// SetDefaultHandler sets a fallback handler when no command-specific handler is registered.
func (m *MockRunner) SetDefaultHandler(handler MockHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaultHandler = handler
}

// Run records the invocation and executes the mock handler, returning stdout bytes.
func (m *MockRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	opt := RunOptions{
		Name:   name,
		Args:   args,
		Stdout: &stdout,
		Stderr: &stderr,
	}
	err := m.RunCustom(ctx, opt)
	return stdout.Bytes(), err
}

// RunCustom records the call and executes the registered handler.
func (m *MockRunner) RunCustom(ctx context.Context, opt RunOptions) error {
	m.mu.Lock()
	m.calls = append(m.calls, opt)
	handler, exists := m.handlers[opt.Name]
	if !exists {
		handler = m.defaultHandler
	}
	m.mu.Unlock()

	if handler != nil {
		return handler(ctx, opt)
	}
	return nil
}

// Calls returns a copy of all recorded command executions.
func (m *MockRunner) Calls() []RunOptions {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make([]RunOptions, len(m.calls))
	copy(copied, m.calls)
	return copied
}

// Reset clears recorded calls and handlers.
func (m *MockRunner) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = nil
	m.handlers = make(map[string]MockHandler)
	m.defaultHandler = nil
}
