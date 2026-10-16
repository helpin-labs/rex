//go:build !windows

package rex

import (
	"context"
	"errors"
)

func initialSandboxStatus() CommandSandboxStatus        { return CommandSandboxStatus{} }
func (s *CommandSandbox) install(context.Context) error { return errors.New("WSL requires Windows") }
func (s *CommandSandbox) RuntimeEnv(context.Context) []string {
	return []string{"AGENT_RUNTIME_EXECUTION_ISOLATION=" + ExecutionIsolation()}
}

func (s *CommandSandbox) Remove(context.Context) error { return errors.New("WSL requires Windows") }
