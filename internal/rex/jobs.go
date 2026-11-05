package rex

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/helpin-ai/agent-runtime-go"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Batch jobs let a bot hand a long list (schools, leads, files) to the
// runtime instead of working through it in one conversation. The runtime
// owns the list: it gives a fresh worker of the same bot ten items at a time,
// accepts each item's result separately, retries what was missed and keeps
// going without anyone typing "continue". Rex records which bot and
// conversation a job belongs to, shows its progress, writes the results into
// the bot's folder and tells the bot when the job needs it.

const (
	jobsServerID      = "rex-jobs"
	maxInlineJobItems = 500
	maxJobItems       = 10000
	jobWatchInterval  = 3 * time.Second
)

type JobRecord struct {
	ID             string    `json:"id"`
	BotID          string    `json:"bot_id"`
	ConversationID string    `json:"conversation_id"`
	Title          string    `json:"title"`
	State          string    `json:"state"`
	DatasetPath    string    `json:"dataset_path,omitempty"`
	NotifiedState  string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// JobView is a job as the UI shows it: Rex's record plus the runtime's live
// state and where the workers are in the list.
type JobView struct {
	JobRecord
	Job *sdk.WorkJob `json:"job,omitempty"`
	// Current lists the items workers are processing right now.
	Current []JobItemRef `json:"current"`
	// ItemsPerMinute is the recent throughput; zero until measurable.
	ItemsPerMinute float64 `json:"items_per_minute"`
	Error          string  `json:"error,omitempty"`
}

// JobItemRef names an item for people: its id and a readable label.
type JobItemRef struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// itemLabel picks a human name for an item from common input fields.
func itemLabel(id string, input json.RawMessage) string {
	var fields map[string]any
	if json.Unmarshal(input, &fields) == nil {
		for _, key := range []string{"name", "title", "label", "company", "school", "domain", "website", "url", "email"} {
			if value, ok := fields[key].(string); ok && strings.TrimSpace(value) != "" {
				label := strings.TrimSpace(value)
				for _, place := range []string{"city", "state"} {
					if extra, ok := fields[place].(string); ok && strings.TrimSpace(extra) != "" && key == "name" {
						label += ", " + strings.TrimSpace(extra)
					}
				}
				return label
			}
		}
	}
	return id
}

// --- store ---

func (s *Store) CreateJob(ctx context.Context, job JobRecord) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO work_jobs(id,bot_id,conversation_id,title,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`,
		job.ID, job.BotID, job.ConversationID, job.Title, job.State, now, now)
	return err
}

const jobColumns = `id,bot_id,conversation_id,title,state,dataset_path,notified_state,created_at,updated_at`

func scanJob(row interface{ Scan(...any) error }) (*JobRecord, error) {
	var job JobRecord
	err := row.Scan(&job.ID, &job.BotID, &job.ConversationID, &job.Title, &job.State, &job.DatasetPath, &job.NotifiedState, &job.CreatedAt, &job.UpdatedAt)
	return &job, err
}

func (s *Store) GetJob(ctx context.Context, id string) (*JobRecord, error) {
	job, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM work_jobs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return job, err
}

// ListJobs returns jobs newest first, for one bot when botID is set.
func (s *Store) ListJobs(ctx context.Context, botID string, limit int) ([]JobRecord, error) {
	query, args := `SELECT `+jobColumns+` FROM work_jobs`, []any{}
	if botID != "" {
		query += ` WHERE bot_id=?`
		args = append(args, botID)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []JobRecord{}
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, *job)
	}
	return jobs, rows.Err()
}

// OpenJobs lists jobs whose final state Rex has not handled yet.
func (s *Store) OpenJobs(ctx context.Context) ([]JobRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM work_jobs WHERE notified_state NOT IN ('completed','failed','cancelled') ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []JobRecord{}
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, *job)
	}
	return jobs, rows.Err()
}

func (s *Store) UpdateJob(ctx context.Context, id, state, datasetPath, notified string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE work_jobs SET state=?, dataset_path=CASE WHEN ?='' THEN dataset_path ELSE ? END, notified_state=CASE WHEN ?='' THEN notified_state ELSE ? END, updated_at=? WHERE id=?`,
		state, datasetPath, datasetPath, notified, notified, time.Now().UTC(), id)
	return err
}

func (s *Store) DeleteJobsForBot(ctx context.Context, botID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM work_jobs WHERE bot_id=?`, botID)
	return err
}

// --- availability ---

var (
	workJobsMu      sync.Mutex
	workJobsChecked time.Time
	workJobsOK      bool
)

// WorkJobsAvailable reports whether the running engine offers work jobs to
// Rex. The answer is cached briefly; it changes only with the engine.
func (h *Host) WorkJobsAvailable(ctx context.Context) bool {
	if !h.RuntimeReady() {
		return false
	}
	workJobsMu.Lock()
	defer workJobsMu.Unlock()
	if time.Since(workJobsChecked) < time.Minute {
		return workJobsOK
	}
	capabilities, err := h.Runtime.GetCapabilities(ctx)
	workJobsOK = err == nil && capabilities.WorkJobs != nil && capabilities.WorkJobs.Supported
	workJobsChecked = time.Now()
	return workJobsOK
}

func resetWorkJobsAvailability() {
	workJobsMu.Lock()
	workJobsChecked = time.Time{}
	workJobsMu.Unlock()
}

// --- the bot's job tools ---

// JobsToken is the bearer token for one bot's job tools.
func (h *Host) JobsToken(botID string) string {
	mac := hmac.New(sha256.New, h.cfg.MCPSecret)
	mac.Write([]byte("bot-jobs:" + botID))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h *Host) jobsMCPServer(bot Bot) RunMCPServer {
	return RunMCPServer{
		ServerID: jobsServerID, ServerName: "jobs", Transport: "streamable_http",
		URL: h.cfg.HostURL + "/mcp/jobs/" + bot.ID,
		// Starting a job spends model quota on the user's behalf, so it is a
		// write: approval-mode bots ask first.
		Tools:      []RunMCPTool{{Name: "start_batch_job", Access: "write"}, {Name: "get_batch_job", Access: "read"}},
		Credential: &RunMCPCredential{Type: "bearer_token", AccessToken: h.JobsToken(bot.ID)},
	}
}

type startJobInput struct {
	Title                   string              `json:"title"`
	Objective               string              `json:"objective"`
	Instructions            string              `json:"instructions"`
	ResultSchema            json.RawMessage     `json:"result_schema"`
	NoResultSchema          json.RawMessage     `json:"no_result_schema,omitempty"`
	ExceptionKinds          []string            `json:"exception_kinds,omitempty"`
	RetryableExceptionKinds []string            `json:"retryable_exception_kinds,omitempty"`
	Items                   []sdk.WorkItemInput `json:"items,omitempty"`
	ItemsFile               string              `json:"items_file,omitempty"`
	BatchSize               int                 `json:"batch_size,omitempty"`
	Workers                 int                 `json:"workers,omitempty"`
}

func (h *Host) jobsMCP() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		botID := strings.TrimPrefix(r.URL.Path, "/mcp/jobs/")
		if botID == "" || !bearerMatches(r, h.JobsToken(botID)) {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
			return
		}
		server := protocol.NewServer(&protocol.Implementation{Name: "rex-jobs", Version: "1.0.0"}, nil)
		server.AddTool(&protocol.Tool{
			Name: "start_batch_job",
			Description: "Start a batch job when the user wants the same work done for many items (more than about 15): researching a list of schools, checking many websites, enriching leads. " +
				"The engine gives fresh copies of you ten items at a time, records each item's result separately, retries anything missed and keeps going until every item is done; the user does not have to say continue. " +
				"Define the result for one item as a JSON Schema, write clear instructions for one item, and pass the items inline (up to 500) or as items_file, a JSON, JSONL or CSV file in your folder (CSV needs an id column). " +
				"Each item needs a stable unique id. Tell the user the job started and that you will report when it finishes; do not process the items yourself.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"title", "objective", "instructions", "result_schema"}, "properties": map[string]any{
				"title":                     map[string]any{"type": "string", "minLength": 3, "maxLength": 120, "description": "Short name the user sees, e.g. \"Find parent forums for 1,000 schools\"."},
				"objective":                 map[string]any{"type": "string", "minLength": 10, "maxLength": 2000, "description": "What the whole job is for."},
				"instructions":              map[string]any{"type": "string", "minLength": 20, "maxLength": 16000, "description": "How to process ONE item: sources to check, what counts, what to record."},
				"result_schema":             map[string]any{"type": "object", "description": "JSON Schema for one found result. Require the fields that make a result trustworthy, such as source URLs and quotes."},
				"no_result_schema":          map[string]any{"type": "object", "description": "Optional JSON Schema for a 'nothing exists' result, e.g. requiring the searches done."},
				"exception_kinds":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Allowed reasons an item cannot be decided, e.g. site_unreachable, blocked, needs_login, ambiguous."},
				"retryable_exception_kinds": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Exception kinds worth retrying later, e.g. site_unreachable."},
				"items": map[string]any{"type": "array", "maxItems": maxInlineJobItems, "items": map[string]any{"type": "object", "required": []string{"id"}, "properties": map[string]any{
					"id": map[string]any{"type": "string"}, "input": map[string]any{"type": "object"},
				}}},
				"items_file": map[string]any{"type": "string", "description": "Path inside your folder to a JSON array, JSONL or CSV file of items."},
				"batch_size": map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "description": "Items per worker; default 10."},
				"workers":    map[string]any{"type": "integer", "minimum": 1, "maximum": 4, "description": "Workers at once; default 1. Browser work always runs one at a time."},
			}},
		}, func(ctx context.Context, request *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			var input startJobInput
			if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
				return toolError(err), nil
			}
			bot, err := h.Store.GetBot(ctx, botID)
			if err != nil {
				return toolError(err), nil
			}
			view, err := h.StartJob(ctx, *bot, input)
			if err != nil {
				return toolError(err), nil
			}
			return toolResult(map[string]any{"job_id": view.ID, "items": view.Job.Counts.Total, "state": view.Job.State,
				"message": "Started. The user sees its progress in the app and you will be told when it finishes. Do not work on the items yourself."}), nil
		})
		server.AddTool(&protocol.Tool{
			Name:        "get_batch_job",
			Description: "Check one of your batch jobs: progress, exceptions and, once results exist, where they are saved in your folder. Omit job_id for your latest job.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"job_id": map[string]any{"type": "string"}}},
		}, func(ctx context.Context, request *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			var input struct {
				JobID string `json:"job_id"`
			}
			_ = json.Unmarshal(request.Params.Arguments, &input)
			record, err := h.botJob(ctx, botID, input.JobID)
			if err != nil {
				return toolError(err), nil
			}
			view := h.jobView(ctx, *record, false)
			if view.Job == nil {
				return toolError(errors.New(view.Error)), nil
			}
			exceptions, _ := h.Runtime.ListWorkItems(ctx, record.ID, "", "", 0)
			summary := map[string]any{"job_id": record.ID, "title": record.Title, "state": view.Job.State, "waiting_reason": view.Job.WaitingReason, "counts": view.Job.Counts,
				"results_file": record.DatasetPath}
			var notes []map[string]any
			if exceptions != nil {
				for _, item := range exceptions.Items {
					if item.Exception != nil && len(notes) < 20 {
						notes = append(notes, map[string]any{"id": item.ID, "kind": item.Exception.Kind, "detail": item.Exception.Detail})
					}
				}
			}
			summary["exceptions_sample"] = notes
			return toolResult(summary), nil
		})
		protocol.NewStreamableHTTPHandler(func(*http.Request) *protocol.Server { return server }, &protocol.StreamableHTTPOptions{Stateless: true, JSONResponse: true}).ServeHTTP(w, r)
	})
}

func (h *Host) botJob(ctx context.Context, botID, jobID string) (*JobRecord, error) {
	if jobID != "" {
		record, err := h.Store.GetJob(ctx, jobID)
		if err != nil || record.BotID != botID {
			return nil, errors.New("job not found")
		}
		return record, nil
	}
	jobs, err := h.Store.ListJobs(ctx, botID, 1)
	if err != nil || len(jobs) == 0 {
		return nil, errors.New("you have no batch jobs")
	}
	return &jobs[0], nil
}

// StartJob creates a runtime work job whose workers are runs of bot, with
// the same model, tools and logins as the bot's own turns, minus the job and
// delegation tools: a worker processes items, it does not start jobs.
func (h *Host) StartJob(ctx context.Context, bot Bot, input startJobInput) (*JobView, error) {
	if !h.WorkJobsAvailable(ctx) {
		return nil, errors.New("the bot engine does not support batch jobs yet")
	}
	items, err := jobItems(bot, input)
	if err != nil {
		return nil, err
	}
	conversation, err := h.Store.ThreadForBot(ctx, bot)
	if err != nil {
		return nil, err
	}
	model, credential, err := h.Settings.RunModelFor(ctx, bot)
	if err != nil {
		return nil, err
	}
	if err := h.SyncBot(ctx, bot); err != nil {
		return nil, fmt.Errorf("sync bot: %w", err)
	}
	agent, _ := h.runAgent(ctx, bot, model)
	if _, err := h.Runtime.UpsertAgent(ctx, agent); err != nil {
		return nil, fmt.Errorf("configure bot: %w", err)
	}
	servers, err := h.runMCPServers(ctx, bot)
	if err != nil {
		return nil, err
	}
	var workerServers []sdk.RunMCPServer
	for _, server := range servers {
		if server.ServerID != jobsServerID && server.ServerID != "rex-bots" && server.ServerID != "rex-setup" {
			workerServers = append(workerServers, server)
		}
	}
	workers := max(1, min(input.Workers, 4))
	spec := sdk.WorkJobSpec{
		Objective: input.Objective, Instructions: input.Instructions, ResultSchema: input.ResultSchema, NoResultSchema: input.NoResultSchema,
		ExceptionKinds: input.ExceptionKinds, RetryableExceptionKinds: input.RetryableExceptionKinds, BatchSize: input.BatchSize,
		MaxConcurrency: workers, SerializeBrowser: true,
	}
	digest := sha256.New()
	_ = json.NewEncoder(digest).Encode([]any{bot.ID, input.Title, input.Objective, items})
	job, err := h.Runtime.CreateWorkJob(ctx, sdk.CreateWorkJobRequest{
		IdempotencyKey: "rex-" + hex.EncodeToString(digest.Sum(nil))[:32], Spec: spec, Items: items,
		Worker: sdk.WorkJobWorker{
			AgentID: bot.ID, Target: sdk.TargetRef{Type: "conversation", ID: conversation.ID},
			Model: model, ModelCredential: credential, MCPServers: workerServers, ExternalActorID: LocalActorID,
			Metadata: map[string]any{"bot_id": bot.ID, "conversation_id": conversation.ID, "trusted_user_messages": h.trustedUserMessages(ctx, conversation.ID)},
		},
	})
	if err != nil {
		return nil, err
	}
	record := JobRecord{ID: job.ID, BotID: bot.ID, ConversationID: conversation.ID, Title: strings.TrimSpace(input.Title), State: job.State}
	if err := h.Store.CreateJob(ctx, record); err != nil {
		return nil, err
	}
	slog.Info("batch job started", "bot", bot.ID, "job", job.ID, "items", job.Counts.Total)
	return &JobView{JobRecord: record, Job: job, Current: []JobItemRef{}}, nil
}

// jobItems reads items given inline or from a file in the bot's folder.
func jobItems(bot Bot, input startJobInput) ([]sdk.WorkItemInput, error) {
	if len(input.Items) > 0 && input.ItemsFile != "" {
		return nil, errors.New("give items or items_file, not both")
	}
	if input.ItemsFile == "" {
		if len(input.Items) == 0 {
			return nil, errors.New("items or items_file is required")
		}
		return input.Items, nil
	}
	path, err := pathInWorkspace(bot.WorkspaceDir, input.ItemsFile)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read items file: %w", err)
	}
	var items []sdk.WorkItemInput
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv":
		items, err = csvItems(raw)
	case ".jsonl", ".ndjson":
		scanner := bufio.NewScanner(bytes.NewReader(raw))
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		for scanner.Scan() {
			if line := bytes.TrimSpace(scanner.Bytes()); len(line) > 0 {
				item, err := objectItem(line, len(items))
				if err != nil {
					return nil, err
				}
				items = append(items, item)
			}
		}
		err = scanner.Err()
	default:
		var objects []json.RawMessage
		if err = json.Unmarshal(raw, &objects); err == nil {
			for i, object := range objects {
				item, itemErr := objectItem(object, i)
				if itemErr != nil {
					return nil, itemErr
				}
				items = append(items, item)
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("items file: %w", err)
	}
	if len(items) == 0 || len(items) > maxJobItems {
		return nil, fmt.Errorf("items file must hold between 1 and %d items", maxJobItems)
	}
	return items, nil
}

// objectItem accepts {"id":..., "input":{...}} or a flat object with an id
// field, which becomes the input itself.
func objectItem(raw json.RawMessage, index int) (sdk.WorkItemInput, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return sdk.WorkItemInput{}, fmt.Errorf("item %d is not an object", index+1)
	}
	var id string
	if err := json.Unmarshal(object["id"], &id); err != nil || strings.TrimSpace(id) == "" {
		var number json.Number
		if json.Unmarshal(object["id"], &number) != nil || number == "" {
			return sdk.WorkItemInput{}, fmt.Errorf("item %d needs an id", index+1)
		}
		id = number.String()
	}
	if input, ok := object["input"]; ok && len(object) <= 3 {
		return sdk.WorkItemInput{ID: id, Input: input}, nil
	}
	return sdk.WorkItemInput{ID: id, Input: raw}, nil
}

func csvItems(raw []byte) ([]sdk.WorkItemInput, error) {
	reader := csv.NewReader(bytes.NewReader(raw))
	header, err := reader.Read()
	if err != nil {
		return nil, err
	}
	idColumn := -1
	for i, name := range header {
		header[i] = strings.TrimSpace(strings.TrimPrefix(name, "\ufeff"))
		if strings.EqualFold(header[i], "id") {
			idColumn = i
		}
	}
	if idColumn < 0 {
		return nil, errors.New("the CSV needs an id column")
	}
	var items []sdk.WorkItemInput
	for {
		row, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return items, nil
		}
		if err != nil {
			return nil, err
		}
		object := map[string]string{}
		for i, value := range row {
			if i < len(header) {
				object[header[i]] = value
			}
		}
		input, _ := json.Marshal(object)
		items = append(items, sdk.WorkItemInput{ID: strings.TrimSpace(row[idColumn]), Input: input})
	}
}

// pathInWorkspace resolves rel inside the bot's folder and refuses escapes.
func pathInWorkspace(workspace, rel string) (string, error) {
	if workspace == "" {
		return "", errors.New("this bot has no folder")
	}
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", err
	}
	path := filepath.Clean(filepath.Join(root, rel))
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("items file: %w", err)
	}
	if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return "", errors.New("items_file must be inside your folder")
	}
	return resolved, nil
}

// --- views and controls ---

// jobView joins Rex's record with the runtime's live job. With detail it
// also reports which items workers are on and the recent throughput.
func (h *Host) jobView(ctx context.Context, record JobRecord, detail bool) JobView {
	view := JobView{JobRecord: record, Current: []JobItemRef{}}
	job, err := h.Runtime.GetWorkJob(ctx, record.ID)
	if err != nil {
		view.Error = "the bot engine is not reachable"
		if !h.RuntimeReady() {
			view.Error = "the bot engine is starting"
		}
		return view
	}
	view.Job, view.State = job, job.State
	if !detail {
		return view
	}
	if job.Counts.Running > 0 {
		if page, err := h.Runtime.ListWorkItems(ctx, record.ID, sdk.WorkItemStateRunning, "", 50); err == nil {
			for _, item := range page.Items {
				view.Current = append(view.Current, JobItemRef{ID: item.ID, Label: itemLabel(item.ID, item.Input)})
			}
		}
	}
	view.ItemsPerMinute = h.jobThroughput(ctx, record.ID)
	return view
}

// jobThroughput measures accepted items per minute over the last 50
// completed batches.
func (h *Host) jobThroughput(ctx context.Context, jobID string) float64 {
	attempts, err := h.Runtime.ListWorkAttempts(ctx, jobID)
	if err != nil {
		return 0
	}
	var first time.Time
	accepted := 0
	for i := max(0, len(attempts)-50); i < len(attempts); i++ {
		attempt := attempts[i]
		if attempt.EndedAt == nil {
			continue
		}
		if first.IsZero() || attempt.StartedAt.Before(first) {
			first = attempt.StartedAt
		}
		accepted += attempt.Accepted
	}
	if first.IsZero() || accepted == 0 {
		return 0
	}
	minutes := time.Since(first).Minutes()
	if minutes < 0.25 {
		return 0
	}
	return float64(accepted) / minutes
}

func (h *Host) listJobs(w http.ResponseWriter, r *http.Request) {
	records, err := h.Store.ListJobs(r.Context(), r.URL.Query().Get("bot_id"), 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	views := make([]JobView, 0, len(records))
	for _, record := range records {
		active := record.NotifiedState != sdk.WorkJobStateCompleted && record.NotifiedState != sdk.WorkJobStateCancelled && record.NotifiedState != sdk.WorkJobStateFailed
		views = append(views, h.jobView(r.Context(), record, active))
	}
	writeJSON(w, http.StatusOK, views)
}

func (h *Host) getJob(w http.ResponseWriter, r *http.Request) {
	record, err := h.Store.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("job not found"))
		return
	}
	writeJSON(w, http.StatusOK, h.jobView(r.Context(), *record, true))
}

func (h *Host) listJobItems(w http.ResponseWriter, r *http.Request) {
	if _, err := h.Store.GetJob(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, errors.New("job not found"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, err := h.Runtime.ListWorkItems(r.Context(), r.PathValue("id"), r.URL.Query().Get("state"), r.URL.Query().Get("after"), limit)
	writeResult(w, page, err)
}

func (h *Host) jobAction(w http.ResponseWriter, r *http.Request) {
	record, err := h.Store.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("job not found"))
		return
	}
	var job *sdk.WorkJob
	switch r.PathValue("action") {
	case "pause":
		job, err = h.Runtime.PauseWorkJob(r.Context(), record.ID)
	case "resume":
		job, err = h.Runtime.ResumeWorkJob(r.Context(), record.ID)
	case "cancel":
		job, err = h.Runtime.CancelWorkJob(r.Context(), record.ID)
	default:
		writeError(w, http.StatusNotFound, errors.New("unknown action"))
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	_ = h.Store.UpdateJob(r.Context(), record.ID, job.State, "", "")
	h.wakeJobs()
	writeJSON(w, http.StatusOK, h.jobView(r.Context(), *record, true))
}

func (h *Host) revealJobResults(w http.ResponseWriter, r *http.Request) {
	record, err := h.Store.GetJob(r.Context(), r.PathValue("id"))
	if err != nil || record.DatasetPath == "" {
		writeError(w, http.StatusNotFound, errors.New("no results yet"))
		return
	}
	bot, err := h.Store.GetBot(r.Context(), record.BotID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeResult(w, map[string]string{"status": "ok"}, revealPath(filepath.Join(bot.WorkspaceDir, record.DatasetPath), true))
}

// --- watcher ---

var jobWake = make(chan struct{}, 1)

func (h *Host) wakeJobs() {
	select {
	case jobWake <- struct{}{}:
	default:
	}
}

var jobWatcherOnce sync.Once

// startJobWatcher follows open jobs for as long as Rex runs: it saves
// results into the bot's folder and tells the bot when a job finishes or
// needs a decision.
func (h *Host) startJobWatcher() {
	jobWatcherOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(jobWatchInterval)
			defer ticker.Stop()
			for {
				if h.RuntimeReady() {
					h.checkJobs(context.Background())
				}
				select {
				case <-ticker.C:
				case <-jobWake:
				}
			}
		}()
	})
}

func (h *Host) checkJobs(ctx context.Context) {
	records, err := h.Store.OpenJobs(ctx)
	if err != nil {
		return
	}
	for _, record := range records {
		job, err := h.Runtime.GetWorkJob(ctx, record.ID)
		if err != nil {
			continue
		}
		notify := ""
		switch {
		case job.State == sdk.WorkJobStateCompleted || job.State == sdk.WorkJobStateCancelled || job.State == sdk.WorkJobStateFailed:
			notify = job.State
		case job.State == sdk.WorkJobStateWaiting && userDecision(job.WaitingReason):
			notify = "waiting:" + job.WaitingReason
		}
		if notify == "" || notify == record.NotifiedState {
			reopened := ""
			if notify == "" && strings.HasPrefix(record.NotifiedState, "waiting:") {
				// The decision was made; a later pause is reported again.
				reopened = "running"
			}
			if job.State != record.State || reopened != "" {
				_ = h.Store.UpdateJob(ctx, record.ID, job.State, "", reopened)
			}
			continue
		}
		datasetPath, err := h.saveJobResults(ctx, record, job)
		if err != nil {
			slog.Warn("could not save batch job results", "job", record.ID, "error", err)
		}
		if err := h.reportJob(ctx, record, job, datasetPath); err != nil {
			if errors.Is(err, ErrBusy) {
				// The bot is mid-turn; report on the next check.
				_ = h.Store.UpdateJob(ctx, record.ID, job.State, datasetPath, "")
				continue
			}
			slog.Warn("could not report batch job", "job", record.ID, "error", err)
		}
		_ = h.Store.UpdateJob(ctx, record.ID, job.State, datasetPath, notify)
	}
}

func userDecision(reason string) bool {
	switch reason {
	case sdk.WorkJobWaitingNoProgress, sdk.WorkJobWaitingExceptions, sdk.WorkJobWaitingBudget, sdk.WorkJobWaitingWorkerAuth, sdk.WorkJobWaitingUncertainEffects:
		return true
	}
	return false
}

// saveJobResults writes the accepted results, joined with each item's
// input, to jobs/<title>.json and .csv in the bot's folder.
func (h *Host) saveJobResults(ctx context.Context, record JobRecord, job *sdk.WorkJob) (string, error) {
	bot, err := h.Store.GetBot(ctx, record.BotID)
	if err != nil || bot.WorkspaceDir == "" {
		return "", err
	}
	type row struct {
		ID        string                 `json:"id"`
		State     string                 `json:"state"`
		Outcome   string                 `json:"outcome,omitempty"`
		Input     json.RawMessage        `json:"input"`
		Result    json.RawMessage        `json:"result,omitempty"`
		Exception *sdk.WorkItemException `json:"exception,omitempty"`
		Error     string                 `json:"error,omitempty"`
	}
	var rows []row
	for after := ""; ; {
		page, err := h.Runtime.ListWorkItems(ctx, record.ID, "", after, 1000)
		if err != nil {
			return "", err
		}
		for _, item := range page.Items {
			r := row{ID: item.ID, State: item.State, Outcome: item.Outcome, Input: item.Input, Result: item.Result, Exception: item.Exception}
			if item.State != sdk.WorkItemStateCompleted {
				r.Error = item.LastError
			}
			rows = append(rows, r)
		}
		if page.NextAfter == "" {
			break
		}
		after = page.NextAfter
	}
	slug := strings.Trim(slugPattern.ReplaceAllString(strings.ToLower(record.Title), "-"), "-")
	if len(slug) > 50 {
		slug = slug[:50]
	}
	rel := filepath.Join("jobs", fmt.Sprintf("%s-%s", defaultString(slug, "job"), record.ID[len(record.ID)-6:]))
	dir := filepath.Join(bot.WorkspaceDir, "jobs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(bot.WorkspaceDir, rel+".json"), raw, 0o644); err != nil {
		return "", err
	}
	var csvBuf bytes.Buffer
	writer := csv.NewWriter(&csvBuf)
	_ = writer.Write([]string{"id", "state", "outcome", "result", "exception_kind", "exception_detail", "error"})
	for _, r := range rows {
		kind, detail := "", ""
		if r.Exception != nil {
			kind, detail = r.Exception.Kind, r.Exception.Detail
		}
		_ = writer.Write([]string{r.ID, r.State, r.Outcome, string(r.Result), kind, detail, r.Error})
	}
	writer.Flush()
	if err := os.WriteFile(filepath.Join(bot.WorkspaceDir, rel+".csv"), csvBuf.Bytes(), 0o644); err != nil {
		return "", err
	}
	return rel + ".json", nil
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// reportJob tells the bot, in its conversation, that a job finished or
// needs a decision, so it can summarize the results or ask the user.
func (h *Host) reportJob(ctx context.Context, record JobRecord, job *sdk.WorkJob, datasetPath string) error {
	c := job.Counts
	counts := fmt.Sprintf("%d of %d items: %d found, %d with no result, %d exceptions, %d failed", c.Completed, c.Total, c.Found, c.NoResult, c.Exceptions, c.Failed)
	var shown, model string
	switch job.State {
	case sdk.WorkJobStateCompleted:
		shown = fmt.Sprintf("Batch job “%s” finished", record.Title)
		model = fmt.Sprintf("Your batch job %q (%s) finished: %s. The results are in %s in your folder (also as CSV); get_batch_job lists exceptions. Summarize the results for the user: what was found, notable exceptions, and anything they should check.", record.Title, record.ID, counts, datasetPath)
	case sdk.WorkJobStateCancelled:
		shown = fmt.Sprintf("Batch job “%s” was cancelled", record.Title)
		model = fmt.Sprintf("Your batch job %q (%s) was cancelled with %s. Accepted results are in %s. Tell the user briefly what was completed.", record.Title, record.ID, counts, datasetPath)
	case sdk.WorkJobStateFailed:
		shown = fmt.Sprintf("Batch job “%s” stopped", record.Title)
		model = fmt.Sprintf("Your batch job %q (%s) stopped with an error: %s. Progress: %s. Explain this to the user and suggest what to change.", record.Title, record.ID, job.Error, counts)
	default:
		shown = fmt.Sprintf("Batch job “%s” needs a decision", record.Title)
		reason := map[string]string{
			sdk.WorkJobWaitingNoProgress:       "several workers in a row could not complete any item",
			sdk.WorkJobWaitingExceptions:       "more items ended as exceptions than the job allows",
			sdk.WorkJobWaitingBudget:           "it reached its token budget",
			sdk.WorkJobWaitingWorkerAuth:       "a worker needs the AI connection to be signed in again",
			sdk.WorkJobWaitingUncertainEffects: "a worker made changes (for example sent or updated something) and stopped before finishing its items, so retrying them could repeat those changes",
		}[job.WaitingReason]
		model = fmt.Sprintf("Your batch job %q (%s) paused because %s. Progress: %s. Results so far are in %s. Explain this to the user with examples from get_batch_job and ask whether to continue (they can press Resume on the job card) or change the approach.", record.Title, record.ID, reason, counts, datasetPath)
	}
	_, _, err := h.deliver(ctx, record.ConversationID, delivery{UserText: shown, ModelText: model, Origin: "job"})
	return err
}
