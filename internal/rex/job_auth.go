package rex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	sdk "github.com/helpin-ai/agent-runtime-go"
	"log/slog"
)

// recoverJobAuthentication covers hidden worker runs as well as the template
// used for subsequent batches. Explicitly paused jobs never resume here.
func (h *Host) recoverJobAuthentication(ctx context.Context, record JobRecord, job *sdk.WorkJob) bool {
	if job.State == sdk.WorkJobStatePaused || job.State == sdk.WorkJobStateCancelled || job.State == sdk.WorkJobStateCompleted || job.State == sdk.WorkJobStateFailed {
		return false
	}
	pending := h.jobPending(ctx, job)
	recovered := false
	for _, entry := range pending {
		if entry.Interaction.InteractionKind != "authentication" {
			continue
		}
		var request struct {
			Provider     string `json:"provider"`
			ServerID     string `json:"server_id"`
			ConnectionID string `json:"connection_id"`
			Fingerprint  string `json:"credential_fingerprint"`
		}
		if json.Unmarshal(entry.Interaction.RequestPayload, &request) != nil {
			continue
		}
		snapshot, err := h.Runtime.WorkJobCredentials(ctx, record.ID)
		if err != nil {
			continue
		}
		update := sdk.UpdateWorkJobCredentialsRequest{ExpectedVersion: snapshot.Version}
		switch request.Provider {
		case "mcp":
			connection, err := h.Store.GetMCPConnection(ctx, request.ServerID)
			if err != nil {
				continue
			}
			// A newly completed sign-in already supplied a fresh token. Do not force
			// another refresh and discard it merely because the old worker paused.
			token, expires, err := h.accessToken(ctx, *connection, false)
			if err != nil {
				continue
			}
			attempted, err := h.Store.rejectedJobRecovery(ctx, record.ID, request.ServerID, entry.Interaction.ID, tokenFingerprint(token))
			if err != nil || (attempted && request.Fingerprint == tokenFingerprint(token)) {
				continue
			}
			if request.Fingerprint != "" && request.Fingerprint == tokenFingerprint(token) {
				if connection.Auth != "oauth" && connection.Auth != "google" {
					continue
				}
				token, expires, err = h.accessToken(ctx, *connection, true)
				if err != nil || tokenFingerprint(token) == request.Fingerprint {
					continue
				}
			}
			credential := sdk.RunMCPCredential{Type: "bearer_token", AccessToken: token, ExpiresAt: expires}
			update.MCP = map[string]sdk.RunMCPCredential{request.ServerID: credential}
		default:
			bot, err := h.Store.GetBot(ctx, record.BotID)
			if err != nil {
				continue
			}
			model, credential, err := h.Settings.RunModelFor(ctx, *bot)
			if err != nil || credential == nil || model == nil || model.Provider != request.Provider || credential.ConnectionID != request.ConnectionID {
				continue
			}
			secret := credential.AccessToken
			if secret == "" {
				secret = credential.APIKey
			}
			if request.Fingerprint != "" && tokenFingerprint(secret) == request.Fingerprint {
				continue
			}
			update.Model = credential
		}
		if _, err := h.Runtime.UpdateWorkJobCredentials(ctx, record.ID, update); err != nil {
			slog.Warn("job credential rotation deferred", "job", record.ID, "run", entry.RunID)
			continue
		}
		if request.Provider == "mcp" {
			err = h.Runtime.RotateMCPCredential(ctx, entry.RunID, request.ServerID, update.MCP[request.ServerID])
		} else {
			_, err = h.Runtime.UpdateRunModelCredential(ctx, entry.RunID, sdk.UpdateRunModelCredentialRequest{Credential: *update.Model})
		}
		if err != nil {
			slog.Warn("worker credential rotation deferred", "job", record.ID, "run", entry.RunID)
			continue
		}
		// Recheck user intent after network calls, before resuming the worker.
		current, err := h.Runtime.GetWorkJob(ctx, record.ID)
		if err != nil || current.State == sdk.WorkJobStatePaused || current.State == sdk.WorkJobStateCancelled {
			continue
		}
		if request.Provider == "mcp" {
			if err := h.Store.recordJobRecovery(ctx, record.ID, request.ServerID, entry.Interaction.ID, tokenFingerprint(update.MCP[request.ServerID].AccessToken)); err != nil {
				continue
			}
		}
		if _, err = h.Runtime.Client.ResumeRun(ctx, entry.RunID, sdk.ResumeRunRequest{Intent: "auth_completed", InteractionID: entry.Interaction.ID, ResumeID: "job-auth-" + entry.Interaction.ID}); err != nil {
			slog.Warn("worker authentication resume deferred", "job", record.ID, "run", entry.RunID)
			continue
		}
		recovered = true
	}
	return recovered
}

type jobBlocker struct {
	RunID         string `json:"run_id"`
	InteractionID string `json:"interaction_id"`
	Kind          string `json:"kind"`
	Provider      string `json:"provider,omitempty"`
	ConnectionID  string `json:"connection_id,omitempty"`
}

func jobBlockers(pending []JobInteraction) []jobBlocker {
	out := []jobBlocker{}
	for _, entry := range pending {
		var request struct {
			Provider     string `json:"provider"`
			ServerID     string `json:"server_id"`
			ConnectionID string `json:"connection_id"`
		}
		if entry.Interaction.InteractionKind == "authentication" {
			_ = json.Unmarshal(entry.Interaction.RequestPayload, &request)
		}
		connection := request.ConnectionID
		if request.Provider == "mcp" {
			connection = request.ServerID
		}
		out = append(out, jobBlocker{RunID: entry.RunID, InteractionID: entry.Interaction.ID, Kind: entry.Interaction.InteractionKind, Provider: request.Provider, ConnectionID: connection})
	}
	return out
}
func jobAuthExplanation(pending []JobInteraction) string {
	for _, blocker := range jobBlockers(pending) {
		if blocker.Kind == "authentication" {
			if blocker.Provider == "mcp" {
				return fmt.Sprintf("the MCP connection %q needs authentication", blocker.ConnectionID)
			}
			if blocker.Provider != "" {
				return fmt.Sprintf("the %s model connection needs authentication", blocker.Provider)
			}
		}
	}
	return "a worker needs authentication; the affected connection has not been identified"
}

// A token that already failed after automatic recovery waits for a new sign-in.
// Retrying the same interaction is safe when the resume acknowledgement was lost.
func (s *Store) rejectedJobRecovery(ctx context.Context, jobID, connectionID, interactionID, fingerprint string) (bool, error) {
	var previousID, previousFingerprint string
	err := s.db.QueryRowContext(ctx, `SELECT interaction_id, fingerprint FROM work_job_auth_recovery WHERE job_id=? AND connection_id=?`, jobID, connectionID).Scan(&previousID, &previousFingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return previousID != interactionID && previousFingerprint == fingerprint, nil
}
func (s *Store) recordJobRecovery(ctx context.Context, jobID, connectionID, interactionID, fingerprint string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO work_job_auth_recovery(job_id,connection_id,interaction_id,fingerprint) VALUES(?,?,?,?) ON CONFLICT(job_id,connection_id) DO UPDATE SET interaction_id=excluded.interaction_id,fingerprint=excluded.fingerprint`, jobID, connectionID, interactionID, fingerprint)
	return err
}
