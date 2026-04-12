package synced

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/csams/todo/embed"
	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
	"github.com/csams/todo/vectorstore"
)

// VectorSyncer is a StoreObserver that keeps a VectorStore in sync with the
// relational store. It also implements SemanticSearcher.
type VectorSyncer struct {
	vs       vectorstore.VectorStore
	embedder embed.Embedder
	store    store.Store // read-only ref for fetching data to embed
	logger   *slog.Logger
}

// New creates a VectorSyncer.
func New(vs vectorstore.VectorStore, embedder embed.Embedder, s store.Store, logger *slog.Logger) *VectorSyncer {
	return &VectorSyncer{
		vs:       vs,
		embedder: embedder,
		store:    s,
		logger:   logger.With("component", "vector-syncer"),
	}
}

// OnEvent handles store events and syncs to the vector store.
// This is best-effort: failures are logged but do not propagate.
func (v *VectorSyncer) OnEvent(ctx context.Context, event store.StoreEvent) {
	var err error
	switch {
	case strings.HasPrefix(event.Type, "task."):
		err = v.syncTasks(ctx, event)
	case strings.HasPrefix(event.Type, "note."):
		err = v.syncNotes(ctx, event)
	}
	if err != nil {
		v.logger.Warn("vector sync failed",
			"event", event.Type,
			"task_ids", event.TaskIDs,
			"error", err,
		)
		// Mark dirty for later reindex
		v.markDirty(ctx, event)
	}
}

func (v *VectorSyncer) syncTasks(ctx context.Context, event store.StoreEvent) error {
	switch event.Type {
	case "task.created", "task.updated", "task.state_changed",
		"task.blockers_added", "task.blockers_removed",
		"task.bulk_state_changed", "task.bulk_priority_changed":
		return v.embedTasks(ctx, event.TaskIDs)

	case "task.archived", "task.unarchived":
		// Re-embed task (updates archived metadata) and all its notes
		if err := v.embedTasks(ctx, event.TaskIDs); err != nil {
			return err
		}
		return v.reembedTaskNotes(ctx, event.TaskIDs)

	case "task.deleted":
		// Delete task and all its notes from vector store
		var ids []string
		for _, tid := range event.TaskIDs {
			ids = append(ids, fmt.Sprintf("task:%d", tid))
			// Also delete associated notes (we don't have note IDs here,
			// so we use a prefix pattern if supported, or rely on reindex cleanup)
		}
		return v.vs.Delete(ctx, ids)

	default:
		return v.embedTasks(ctx, event.TaskIDs)
	}
}

func (v *VectorSyncer) syncNotes(ctx context.Context, event store.StoreEvent) error {
	switch event.Type {
	case "note.created", "note.updated":
		return v.embedNotes(ctx, event.TaskIDs, event.NoteIDs)
	case "note.deleted":
		var ids []string
		for _, nid := range event.NoteIDs {
			ids = append(ids, fmt.Sprintf("note:%d", nid))
		}
		return v.vs.Delete(ctx, ids)
	default:
		return nil
	}
}

func (v *VectorSyncer) embedTasks(ctx context.Context, taskIDs []uint) error {
	var docs []vectorstore.Document
	var texts []string

	for _, tid := range taskIDs {
		detail, err := v.store.GetTask(ctx, tid)
		if err != nil {
			continue // task may have been deleted
		}
		t := detail.Task
		text := buildTaskEmbedText(t)
		texts = append(texts, text)
		docs = append(docs, vectorstore.Document{
			ID:   fmt.Sprintf("task:%d", t.ID),
			Text: text,
			Metadata: map[string]any{
				"type":     "task",
				"task_id":  int(t.ID),
				"state":    string(t.State),
				"priority": t.Priority,
				"archived": t.Archived,
			},
		})
	}

	if len(docs) == 0 {
		return nil
	}

	vecs, err := v.embedder.EmbedBatch(ctx, texts)
	if err != nil {
		return err
	}
	if len(vecs) != len(docs) {
		return fmt.Errorf("embedding count mismatch: got %d vectors for %d documents", len(vecs), len(docs))
	}

	for i := range docs {
		docs[i].Vector = vecs[i]
	}

	return v.vs.Upsert(ctx, docs)
}

func (v *VectorSyncer) embedNotes(ctx context.Context, taskIDs []uint, noteIDs []uint) error {
	if len(taskIDs) != len(noteIDs) {
		return fmt.Errorf("taskIDs/noteIDs length mismatch: %d vs %d", len(taskIDs), len(noteIDs))
	}

	var docs []vectorstore.Document
	var texts []string

	// Cache ListNotes and task archived status by taskID to avoid duplicate calls
	noteCache := map[uint][]model.Note{}
	archivedCache := map[uint]bool{}
	for i, nid := range noteIDs {
		taskID := taskIDs[i]

		if _, cached := noteCache[taskID]; !cached {
			notes, err := v.store.ListNotes(ctx, taskID)
			if err != nil {
				continue
			}
			noteCache[taskID] = notes
		}
		if _, cached := archivedCache[taskID]; !cached {
			detail, err := v.store.GetTask(ctx, taskID)
			if err != nil {
				continue
			}
			archivedCache[taskID] = detail.Archived
		}

		for _, n := range noteCache[taskID] {
			if n.ID == nid {
				texts = append(texts, n.Text)
				docs = append(docs, vectorstore.Document{
					ID:   fmt.Sprintf("note:%d", n.ID),
					Text: n.Text,
					Metadata: map[string]any{
						"type":     "note",
						"task_id":  int(n.TaskID),
						"note_id":  int(n.ID),
						"archived": archivedCache[taskID],
					},
				})
				break
			}
		}
	}

	if len(docs) == 0 {
		return nil
	}

	vecs, err := v.embedder.EmbedBatch(ctx, texts)
	if err != nil {
		return err
	}
	if len(vecs) != len(docs) {
		return fmt.Errorf("embedding count mismatch: got %d vectors for %d documents", len(vecs), len(docs))
	}

	for i := range docs {
		docs[i].Vector = vecs[i]
	}

	return v.vs.Upsert(ctx, docs)
}

// reembedTaskNotes re-embeds all notes for the given tasks, updating their metadata.
func (v *VectorSyncer) reembedTaskNotes(ctx context.Context, taskIDs []uint) error {
	var allTaskIDs, allNoteIDs []uint
	for _, tid := range taskIDs {
		notes, err := v.store.ListNotes(ctx, tid)
		if err != nil {
			continue
		}
		for _, n := range notes {
			allTaskIDs = append(allTaskIDs, tid)
			allNoteIDs = append(allNoteIDs, n.ID)
		}
	}
	if len(allNoteIDs) == 0 {
		return nil
	}
	return v.embedNotes(ctx, allTaskIDs, allNoteIDs)
}

// markDirty logs a warning when vector sync fails so operators know to reindex.
// TODO: Implement full recovery by setting VectorDirty=true on affected records
// (model.Task.VectorDirty, model.Note.VectorDirty) and auto-retrying.
func (v *VectorSyncer) markDirty(_ context.Context, event store.StoreEvent) {
	v.logger.Warn("vector sync failed for entities; run 'todo vector reindex' to recover",
		"task_ids", event.TaskIDs,
		"note_ids", event.NoteIDs,
	)
}

// buildTaskEmbedText creates the enriched text for task embedding.
func buildTaskEmbedText(t model.Task) string {
	var b strings.Builder
	b.WriteString(t.Title)
	if t.Description != "" {
		b.WriteString(". ")
		b.WriteString(t.Description)
	}
	if len(t.Tags) > 0 {
		b.WriteString(". Tags: ")
		tags := make([]string, len(t.Tags))
		for i, tt := range t.Tags {
			tags[i] = tt.Tag
		}
		b.WriteString(strings.Join(tags, ", "))
	}
	fmt.Fprintf(&b, ". Priority: %d. State: %s.", t.Priority, t.State)
	return b.String()
}

// --- SemanticSearcher implementation ---

func (v *VectorSyncer) SemanticSearch(ctx context.Context, query string, opts store.SemanticSearchOptions) ([]store.SemanticSearchResult, error) {
	vec, err := v.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embedding query: %w", err)
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}

	filter := vectorstore.SearchFilter{}
	if opts.Type != "" {
		filter.Type = &opts.Type
	}
	if opts.TaskID != nil {
		filter.TaskID = opts.TaskID
	}
	if !opts.IncludeArchived {
		f := false
		filter.Archived = &f
	}

	results, err := v.vs.Search(ctx, vec, limit, filter)
	if err != nil {
		return nil, err
	}

	return toSemanticResults(results), nil
}

func (v *VectorSyncer) SemanticSearchContext(ctx context.Context, taskID uint, opts store.SemanticSearchOptions) ([]store.SemanticSearchResult, error) {
	// Aggregate task text + all note texts into a single query
	detail, err := v.store.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString(detail.Title)
	if detail.Description != "" {
		b.WriteString(". ")
		b.WriteString(detail.Description)
	}
	for _, n := range detail.Notes {
		b.WriteString(". ")
		b.WriteString(n.Text)
	}

	vec, err := v.embedder.Embed(ctx, b.String())
	if err != nil {
		return nil, fmt.Errorf("embedding context: %w", err)
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}

	// Exclude the source task's own documents
	excludeIDs := []string{fmt.Sprintf("task:%d", taskID)}
	for _, n := range detail.Notes {
		excludeIDs = append(excludeIDs, fmt.Sprintf("note:%d", n.ID))
	}

	filter := vectorstore.SearchFilter{
		ExcludeIDs: excludeIDs,
	}
	if opts.Type != "" {
		filter.Type = &opts.Type
	}
	if !opts.IncludeArchived {
		f := false
		filter.Archived = &f
	}

	results, err := v.vs.Search(ctx, vec, limit+len(excludeIDs), filter)
	if err != nil {
		return nil, err
	}

	// Trim to requested limit (we over-fetched to account for exclusions done server-side)
	if len(results) > limit {
		results = results[:limit]
	}

	return toSemanticResults(results), nil
}

// Reindex re-embeds all tasks and notes from the relational store into the vector store.
func (v *VectorSyncer) Reindex(ctx context.Context, clear bool, progressFn func(done, total int)) error {
	if clear {
		if err := v.vs.Reset(ctx, v.embedder.ModelName(), v.embedder.Dimensions()); err != nil {
			return fmt.Errorf("reset vector store: %w", err)
		}
	}

	// Fetch all tasks
	tasks, err := v.store.ListTasks(ctx, store.ListTasksOptions{
		IncludeArchived: true,
		IncludeSubtasks: true,
	})
	if err != nil {
		return fmt.Errorf("listing tasks: %w", err)
	}

	// Build task archived lookup
	taskArchived := make(map[uint]bool, len(tasks))
	for _, t := range tasks {
		taskArchived[t.ID] = t.Archived
	}

	// Fetch all notes for all tasks
	type noteEntry struct {
		note   model.Note
		taskID uint
	}
	var allNotes []noteEntry
	for _, t := range tasks {
		notes, err := v.store.ListNotes(ctx, t.ID)
		if err != nil {
			continue
		}
		for _, n := range notes {
			allNotes = append(allNotes, noteEntry{note: n, taskID: t.ID})
		}
	}

	total := len(tasks) + len(allNotes)
	done := 0

	// Embed tasks in batches of 100
	batchSize := 100
	for i := 0; i < len(tasks); i += batchSize {
		end := i + batchSize
		if end > len(tasks) {
			end = len(tasks)
		}
		batch := tasks[i:end]

		var docs []vectorstore.Document
		var texts []string
		for _, t := range batch {
			text := buildTaskEmbedText(t)
			texts = append(texts, text)
			docs = append(docs, vectorstore.Document{
				ID:   fmt.Sprintf("task:%d", t.ID),
				Text: text,
				Metadata: map[string]any{
					"type":     "task",
					"task_id":  int(t.ID),
					"state":    string(t.State),
					"priority": t.Priority,
					"archived": t.Archived,
				},
			})
		}

		vecs, err := v.embedder.EmbedBatch(ctx, texts)
		if err != nil {
			return fmt.Errorf("embedding tasks batch %d: %w", i/batchSize, err)
		}
		if len(vecs) != len(docs) {
			return fmt.Errorf("embedding tasks batch %d: got %d vectors for %d documents", i/batchSize, len(vecs), len(docs))
		}
		for j := range docs {
			docs[j].Vector = vecs[j]
		}
		if err := v.vs.Upsert(ctx, docs); err != nil {
			return fmt.Errorf("upserting tasks batch %d: %w", i/batchSize, err)
		}

		done += len(batch)
		if progressFn != nil {
			progressFn(done, total)
		}
	}

	// Embed notes in batches of 100
	for i := 0; i < len(allNotes); i += batchSize {
		end := i + batchSize
		if end > len(allNotes) {
			end = len(allNotes)
		}
		batch := allNotes[i:end]

		var docs []vectorstore.Document
		var texts []string
		for _, ne := range batch {
			texts = append(texts, ne.note.Text)
			docs = append(docs, vectorstore.Document{
				ID:   fmt.Sprintf("note:%d", ne.note.ID),
				Text: ne.note.Text,
				Metadata: map[string]any{
					"type":     "note",
					"task_id":  int(ne.taskID),
					"note_id":  int(ne.note.ID),
					"archived": taskArchived[ne.taskID],
				},
			})
		}

		vecs, err := v.embedder.EmbedBatch(ctx, texts)
		if err != nil {
			return fmt.Errorf("embedding notes batch %d: %w", i/batchSize, err)
		}
		if len(vecs) != len(docs) {
			return fmt.Errorf("embedding notes batch %d: got %d vectors for %d documents", i/batchSize, len(vecs), len(docs))
		}
		for j := range docs {
			docs[j].Vector = vecs[j]
		}
		if err := v.vs.Upsert(ctx, docs); err != nil {
			return fmt.Errorf("upserting notes batch %d: %w", i/batchSize, err)
		}

		done += len(batch)
		if progressFn != nil {
			progressFn(done, total)
		}
	}

	return nil
}

func toSemanticResults(results []vectorstore.SearchResult) []store.SemanticSearchResult {
	out := make([]store.SemanticSearchResult, len(results))
	for i, r := range results {
		out[i] = store.SemanticSearchResult{
			ID:       r.ID,
			Text:     r.Text,
			Metadata: r.Metadata,
			Score:    r.Score,
		}
	}
	return out
}

// Compile-time interface checks.
var _ store.StoreObserver = (*VectorSyncer)(nil)
var _ store.SemanticSearcher = (*VectorSyncer)(nil)
