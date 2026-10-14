package rex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	sdk "github.com/helpin-ai/agent-runtime-go"
	"github.com/helpin-ai/agent-runtime/memory"
)

type remoteLearningKey struct{}

func memoryNoteRequest(note MemoryNote) memory.RetainRequest {
	return memory.RetainRequest{DocumentID: noteDocument(note.ID), Content: note.Content, Timestamp: note.CreatedAt,
		Context: "A fact or preference the user explicitly asked to remember. It supersedes earlier conflicting memories.", Metadata: map[string]string{"source": "note"}}
}

func (m *MemoryService) learningClient() (*sdk.Client, error) {
	if m.host == nil || m.host.Runtime == nil {
		return nil, errors.New("bot engine is not ready for learning")
	}
	return m.host.Runtime.Client, nil
}

// Eligibility is host policy, not persisted run state in the runtime queue.
// Keep it alive independently of configuration and long model callbacks.
func (m *MemoryService) learningPermissions(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if client, err := m.learningClient(); err == nil {
			ready := m.Available() && m.host.RuntimeReady() && m.learningReady(ctx)
			callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = client.SetMemoryLearningPolicy(callCtx, sdk.MemoryLearningPolicy{AllowExtraction: ready, AllowConsolidation: ready && m.idleLearningReady(ctx)})
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Import by source/revision, so restarts and repeated notifications cannot
// re-run completed work. Existing records remain the UI/history projection.
func (m *MemoryService) syncLearningJobs(ctx context.Context) error {
	client, err := m.learningClient()
	if err != nil {
		return err
	}
	rows, err := m.store.db.QueryContext(ctx, `SELECT `+episodeColumns+` FROM task_episodes e WHERE forgotten=0 AND revision<>'' AND status IN ('queued','extracting','failed') AND EXISTS(SELECT 1 FROM turns t WHERE t.id=e.turn_id AND t.status<>'working') ORDER BY created_at`)
	if err != nil {
		return err
	}
	var episodes []TaskEpisode
	for rows.Next() {
		e, scanErr := scanEpisode(rows)
		if scanErr != nil {
			err = scanErr
			break
		}
		episodes = append(episodes, e)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	banks := map[string]bool{}
	for _, e := range episodes {
		state := "queued"
		if e.Status == "failed" {
			state = "failed"
		}
		job, err := client.SubmitMemoryLearning(ctx, sdk.MemoryLearningJob{BankID: e.Bank, Kind: "extraction", SourceID: episodeDocument(e.TurnID), Revision: e.Revision, State: state, Attempts: e.Attempts, RetryAt: e.RetryAt, Error: e.Error})
		if err != nil {
			return err
		}
		if job.State == "failed" && e.Status == "queued" && e.Attempts == 0 && e.Error == "" {
			if err = client.RetryMemoryLearning(ctx, job.ID); err != nil {
				return err
			}
			job.State = "queued"
			job.Attempts = 0
			job.RetryAt = nil
			job.Error = ""
		}
		if err = m.projectLearningJob(ctx, job); err != nil {
			return err
		}
		banks[e.Bank] = true
	}
	notes, err := m.store.MemoryNotes(ctx, "", "pending")
	if err != nil {
		return err
	}
	for _, note := range notes {
		digest := sha256.Sum256([]byte(note.Content))
		job, err := client.SubmitMemoryLearning(ctx, sdk.MemoryLearningJob{BankID: note.Bank, Kind: "extraction", SourceID: noteDocument(note.ID), Revision: hex.EncodeToString(digest[:])})
		if err != nil {
			return err
		}
		if err = m.projectLearningJob(ctx, job); err != nil {
			return err
		}
		banks[note.Bank] = true
	}
	for _, bank := range m.Banks(ctx) {
		banks[bank] = true
	}
	for bank := range banks {
		if m.Available() {
			if _, err = m.queueConsolidation(ctx, client, bank, false); err != nil {
				return err
			}
		}
		jobs, err := client.MemoryLearningJobs(ctx, bank)
		if err != nil {
			return err
		}
		for _, job := range jobs {
			if err = m.projectLearningJob(ctx, job); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *MemoryService) queueConsolidation(ctx context.Context, client *sdk.Client, bank string, manual bool) (sdk.MemoryLearningJob, error) {
	if !validBank(bank) {
		return sdk.MemoryLearningJob{}, errors.New("unknown memory bank")
	}
	m.mu.RLock()
	reader := m.reader
	m.mu.RUnlock()
	if reader == nil {
		return sdk.MemoryLearningJob{}, errors.New("memory is not available")
	}
	rows, err := reader.QueryContext(ctx, `SELECT f.id FROM memory_facts f WHERE app_id=? AND bank_id=? AND NOT EXISTS(SELECT 1 FROM memory_consolidated c WHERE c.fact_id=f.id) ORDER BY f.id`, AppID, bank)
	if err != nil {
		return sdk.MemoryLearningJob{}, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return sdk.MemoryLearningJob{}, err
	}
	prior, err := m.store.maintenance(ctx, bank)
	if err != nil {
		return sdk.MemoryLearningJob{}, err
	}
	if len(ids) == 0 {
		jobs, err := client.MemoryLearningJobs(ctx, bank)
		if err != nil {
			return sdk.MemoryLearningJob{}, err
		}
		for _, job := range jobs {
			if job.Kind == "consolidation" {
				return job, nil
			}
		}
	}
	if len(ids) == 0 && !manual && prior.LastSuccess == nil {
		return sdk.MemoryLearningJob{}, nil
	}
	digest := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	state := "consolidation_pending"
	if len(ids) == 0 && !manual {
		state = "consolidated"
	}
	if prior.Attempts >= 3 && !manual {
		state = "failed"
	}
	job, err := client.SubmitMemoryLearning(ctx, sdk.MemoryLearningJob{BankID: bank, Kind: "consolidation", SourceID: "bank", Revision: hex.EncodeToString(digest[:]), State: state, Attempts: prior.Attempts, RetryAt: prior.RetryAt, LastSuccess: prior.LastSuccess, Manual: manual, Error: prior.Error,
		Result: sdk.MemoryLearningResult{Processed: prior.Result.Processed, Created: prior.Result.Created, Updated: prior.Result.Updated, Deleted: prior.Result.Deleted}})
	if err == nil && !manual && job.State == "failed" && prior.Attempts == 0 && prior.Error == "" {
		err = client.RetryMemoryLearning(ctx, job.ID)
		if err == nil {
			job.State = "consolidation_pending"
			job.Attempts = 0
			job.RetryAt = nil
			job.Error = ""
		}
	}
	return job, err
}

func (m *MemoryService) requestConsolidation(ctx context.Context, bank string) (sdk.MemoryLearningJob, error) {
	var pending int
	if err := m.store.db.QueryRowContext(ctx, `SELECT count(*) FROM task_episodes WHERE bank=? AND forgotten=0 AND status IN ('capture','captured','queued','extracting')`, bank).Scan(&pending); err != nil {
		return sdk.MemoryLearningJob{}, err
	}
	if pending > 0 {
		return sdk.MemoryLearningJob{}, fmt.Errorf("extraction is pending for %d tasks; consolidation runs after their facts are extracted", pending)
	}
	client, err := m.learningClient()
	if err != nil {
		return sdk.MemoryLearningJob{}, err
	}
	job, err := m.queueConsolidation(ctx, client, bank, true)
	if err == nil && (job.State == "failed" || job.State == "cancelled") {
		err = client.RetryMemoryLearning(ctx, job.ID)
		if err == nil {
			job.State = "consolidation_pending"
			job.Attempts = 0
			job.RetryAt = nil
			job.Error = ""
			job.Manual = true
		}
	}
	return job, err
}

func (m *MemoryService) projectLearningJob(ctx context.Context, job sdk.MemoryLearningJob) error {
	if job.AppID != AppID || !validBank(job.BankID) {
		return errors.New("memory job belongs to another app or bank")
	}
	if job.Kind == "consolidation" {
		result, _ := json.Marshal(map[string]int{"processed": job.Result.Processed, "created": job.Result.Created, "updated": job.Result.Updated, "deleted": job.Result.Deleted})
		_, err := m.store.db.ExecContext(ctx, `INSERT INTO memory_maintenance(bank,last_success,retry_at,attempts,error,result) VALUES(?,?,?,?,?,?) ON CONFLICT(bank) DO UPDATE SET last_success=COALESCE(excluded.last_success,last_success),retry_at=excluded.retry_at,attempts=excluded.attempts,error=excluded.error,result=excluded.result`, job.BankID, job.LastSuccess, job.RetryAt, job.Attempts, job.Error, string(result))
		return err
	}
	if strings.HasPrefix(job.SourceID, "note/") {
		if m.store.memoryForgotten(ctx, job.BankID, job.SourceID) {
			return nil
		}
		state := "pending"
		if job.State == "indexed" || job.State == "no_lesson" {
			state = "retained"
		} else if job.State == "failed" {
			state = "failed"
		}
		if job.State == "cancelled" {
			state = "retained"
		}
		_, err := m.store.db.ExecContext(ctx, `UPDATE memory_notes SET status=?,error=?,facts=? WHERE id=? AND bank=? AND status<>'forgotten'`, state, job.Error, job.Result.Facts, strings.TrimPrefix(job.SourceID, "note/"), job.BankID)
		return err
	}
	state := job.State
	if state == "cancelled" {
		state = "no_lesson"
	}
	_, err := m.store.db.ExecContext(ctx, `UPDATE task_episodes SET status=?,attempts=?,retry_at=?,error=?,facts=? WHERE turn_id=? AND bank=? AND revision=? AND forgotten=0`, state, job.Attempts, job.RetryAt, job.Error, job.Result.Facts, strings.TrimPrefix(job.SourceID, "episode/"), job.BankID, job.Revision)
	return err
}

func (m *MemoryService) retryLearning(ctx context.Context, bank, source string) error {
	client, err := m.learningClient()
	if err != nil {
		return err
	}
	jobs, err := client.MemoryLearningJobs(ctx, bank)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if job.Kind == "extraction" && job.SourceID == source && (job.State == "failed" || job.State == "cancelled") {
			return client.RetryMemoryLearning(ctx, job.ID)
		}
	}
	return nil
}

// This authenticated host callback supplies product evidence and inference.
// Claiming, retrying and recovering the job belongs exclusively to the runtime.
func (h *Host) processMemoryLearning(w http.ResponseWriter, r *http.Request) {
	var job sdk.MemoryLearningJob
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&job); err != nil {
		writeError(w, 400, errors.New("invalid memory learning job"))
		return
	}
	if job.AppID != AppID || !validBank(job.BankID) {
		writeError(w, 400, errors.New("invalid memory scope"))
		return
	}
	m := h.Memory
	ctx := context.WithValue(r.Context(), remoteLearningKey{}, job)
	var out sdk.MemoryLearningResult
	var err error
	if !m.Available() || !m.learningReady(ctx) {
		out.Interrupted = true
		writeJSON(w, 200, out)
		return
	}
	if job.Kind == "consolidation" {
		result, callErr := m.consolidateBank(ctx, job.BankID)
		err = callErr
		out.Processed, out.Created, out.Updated, out.Deleted = result.Processed, result.Created, result.Updated, result.Deleted
	} else if job.Kind == "extraction" {
		m.workMu.RLock()
		defer m.workMu.RUnlock()
		ctx, cancel := m.learningContext(ctx)
		defer cancel()
		if strings.HasPrefix(job.SourceID, "episode/") {
			e, readErr := m.store.Episode(ctx, strings.TrimPrefix(job.SourceID, "episode/"))
			if readErr != nil || e.Bank != job.BankID || e.Revision != job.Revision || e.Forgotten {
				out.InvalidSource = true
				writeJSON(w, 200, out)
				return
			}
			out.Facts, err = m.extractEpisode(ctx, e)
			current, readErr := m.store.Episode(context.WithoutCancel(ctx), e.TurnID)
			if readErr != nil {
				err = readErr
			} else if current.Forgotten || current.Revision != job.Revision {
				out.InvalidSource = true
				err = nil
				if m.backend != nil {
					_ = m.backend.InvalidateDocument(context.WithoutCancel(ctx), scope(job.BankID), job.SourceID)
				}
			}

		} else if strings.HasPrefix(job.SourceID, "note/") {
			notes, readErr := m.store.MemoryNotes(ctx, job.BankID, "")
			if readErr != nil {
				err = readErr
			} else {
				out.InvalidSource = true
				for _, note := range notes {
					if noteDocument(note.ID) != job.SourceID {
						continue
					}
					digest := sha256.Sum256([]byte(note.Content))
					if hex.EncodeToString(digest[:]) != job.Revision || note.Status == "forgotten" {
						break
					}
					out.InvalidSource = false
					out.Facts, err = m.retain(ctx, job.BankID, memoryNoteRequest(note))
					if err == nil && note.Replaces != "" {
						err = m.Forget(ctx, note.Bank, note.Replaces)
					}
					break
				}
			}
		} else {
			err = errors.New("unknown memory evidence source")
		}
		if ctx.Err() != nil {
			out.Interrupted = true
			err = nil
		}
	} else {
		err = errors.New("unknown memory job kind")
	}
	if errors.Is(err, context.Canceled) {
		out.Interrupted = true
		err = nil
	}
	if err != nil {
		out.Error = redactSecrets(err.Error())
	}
	writeJSON(w, 200, out)
}
