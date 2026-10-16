package rex

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

type CommandSandboxStatus struct {
	Supported bool   `json:"supported"`
	State     string `json:"state"`
	Message   string `json:"message"`
	Busy      bool   `json:"busy"`
}

type CommandSandbox struct {
	dataDir, resources string
	mu                 sync.Mutex
	status             CommandSandboxStatus
}

func NewCommandSandbox(dataDir, resources string) *CommandSandbox {
	return &CommandSandbox{dataDir: dataDir, resources: resources, status: initialSandboxStatus()}
}

func (s *CommandSandbox) Status() CommandSandboxStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *CommandSandbox) setStatus(state, message string, busy bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.State, s.status.Message, s.status.Busy = state, message, busy
}

func (h *Host) commandSandboxStatus(w http.ResponseWriter, r *http.Request) {
	if h.Sandbox == nil {
		writeJSON(w, http.StatusOK, CommandSandboxStatus{})
		return
	}
	writeJSON(w, http.StatusOK, h.Sandbox.Status())
}

func (h *Host) setupCommandSandbox(w http.ResponseWriter, r *http.Request) {
	if h.Sandbox == nil || !h.Sandbox.Status().Supported {
		writeError(w, http.StatusBadRequest, errors.New("WSL setup is only available on Windows"))
		return
	}
	if h.ActiveRuns() != 0 {
		writeError(w, http.StatusConflict, errors.New("finish or cancel active tasks before changing the command sandbox"))
		return
	}
	s := h.Sandbox
	s.mu.Lock()
	if s.status.Busy {
		s.mu.Unlock()
		writeError(w, http.StatusConflict, errors.New("command sandbox setup is already running"))
		return
	}
	s.status.Busy, s.status.State, s.status.Message = true, "installing", "Preparing a separate Linux environment. Your other WSL distributions will not be changed."
	s.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		if err := s.install(ctx); err != nil {
			s.setStatus("error", err.Error(), false)
			return
		}
		s.setStatus("restart_required", "Sandbox verified. Restart Rex when your tasks have finished to activate it.", false)
		if h.ActiveRuns() == 0 {
			h.RequestRuntimeRestart()
		}
	}()
	writeJSON(w, http.StatusAccepted, s.Status())
}
