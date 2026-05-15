package synced

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/csams/todo/embed"
	"github.com/csams/todo/embed/chunker"
	"github.com/csams/todo/model"
	"github.com/csams/todo/store"
	"github.com/csams/todo/vectorstore"
)

// Chunk sizing constants. Targets ~1000 tokens per chunk under a ~3 chars/token
// heuristic, well below nomic-embed-text's 2048-token training window.
const (
	chunkMaxRunes  = 3000
	chunkOverlap   = 200
	headerMaxRunes = 1000 // cap header so a pathological tag list can't starve the body budget
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
	case strings.HasPrefix(event.Type, "link."):
		err = v.syncLinks(ctx, event)
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
		// Delete all chunks for each task. Note rows for these tasks are not
		// removed here — note.deleted events handle that path explicitly when
		// the caller passed delete_notes:true; otherwise notes are orphaned
		// and stay in the index under their own doc ids.
		for _, tid := range event.TaskIDs {
			if err := v.vs.DeleteTaskDocs(ctx, tid); err != nil {
				return err
			}
		}
		return nil

	default:
		return v.embedTasks(ctx, event.TaskIDs)
	}
}

// syncLinks re-embeds the parent task whenever any of its links change,
// because the task's embedding text includes each link's description.
func (v *VectorSyncer) syncLinks(ctx context.Context, event store.StoreEvent) error {
	return v.embedTasks(ctx, event.TaskIDs)
}

func (v *VectorSyncer) syncNotes(ctx context.Context, event store.StoreEvent) error {
	switch event.Type {
	case "note.created", "note.updated", "note.archived", "note.unarchived":
		return v.embedNotes(ctx, event.NoteIDs)
	case "note.deleted":
		for _, nid := range event.NoteIDs {
			if err := v.vs.DeleteNoteDocs(ctx, nid); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}

func (v *VectorSyncer) embedTasks(ctx context.Context, taskIDs []uint) error {
	var docs []vectorstore.Document
	var texts []string

	for _, tid := range taskIDs {
		detail, err := v.store.GetTask(ctx, tid, store.GetTaskOptions{
			Include: map[string]bool{"description": true, "links": true},
		})
		if err != nil {
			continue // task may have been deleted
		}
		t := detail.Task
		// Replace any prior chunks before re-embedding, so a shrunk description
		// can't leave stale chunks behind.
		if err := v.vs.DeleteTaskDocs(ctx, t.ID); err != nil {
			return err
		}
		chunks := buildTaskChunks(t)
		for _, c := range chunks {
			texts = append(texts, c.Text)
			docs = append(docs, vectorstore.Document{
				ID:         fmt.Sprintf("task:%d:%d", t.ID, c.ChunkIndex),
				Text:       c.Text,
				ChunkIndex: c.ChunkIndex,
				Metadata: map[string]any{
					"type":     "task",
					"task_id":  int(t.ID),
					"state":    string(t.State),
					"priority": t.Priority,
					"archived": t.Archived,
				},
			})
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

func (v *VectorSyncer) embedNotes(ctx context.Context, noteIDs []uint) error {
	if len(noteIDs) == 0 {
		return nil
	}
	notes, err := v.store.GetNotesByIDs(ctx, noteIDs)
	if err != nil {
		return fmt.Errorf("loading notes: %w", err)
	}
	if len(notes) == 0 {
		return nil
	}

	// Cache parent task (archived + title) for task-attached notes. One GetTask
	// per parent per batch; not-found tasks (orphan with stale task_id during
	// a race) are treated as standalone for metadata purposes.
	type parentInfo struct {
		archived bool
		title    string
	}
	parentCache := map[uint]parentInfo{}
	missingCache := map[uint]bool{}
	getParent := func(taskID uint) (parentInfo, bool) {
		if p, ok := parentCache[taskID]; ok {
			return p, true
		}
		if missingCache[taskID] {
			return parentInfo{}, false
		}
		detail, err := v.store.GetTask(ctx, taskID, store.GetTaskOptions{})
		if err != nil {
			missingCache[taskID] = true
			return parentInfo{}, false
		}
		p := parentInfo{archived: detail.Archived, title: detail.Title}
		parentCache[taskID] = p
		return p, true
	}

	var docs []vectorstore.Document
	var texts []string
	for _, n := range notes {
		// Replace prior chunks for this note.
		if err := v.vs.DeleteNoteDocs(ctx, n.ID); err != nil {
			return err
		}

		meta := map[string]any{
			"type":    "note",
			"note_id": int(n.ID),
		}
		var parentTitle string
		if n.TaskID != nil {
			if p, found := getParent(*n.TaskID); found {
				meta["task_id"] = int(*n.TaskID)
				meta["archived"] = p.archived
				parentTitle = p.title
			} else {
				// Stale task_id (parent missing); treat as standalone.
				meta["archived"] = n.Archived
			}
		} else {
			meta["archived"] = n.Archived
		}

		chunks := buildNoteChunks(n, parentTitle)
		for _, c := range chunks {
			// Each chunk needs its own metadata copy since Upsert mutates per-doc.
			cmeta := make(map[string]any, len(meta))
			for k, v := range meta {
				cmeta[k] = v
			}
			texts = append(texts, c.Text)
			docs = append(docs, vectorstore.Document{
				ID:         fmt.Sprintf("note:%d:%d", n.ID, c.ChunkIndex),
				Text:       c.Text,
				ChunkIndex: c.ChunkIndex,
				Metadata:   cmeta,
			})
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
	var allNoteIDs []uint
	for _, tid := range taskIDs {
		t := tid
		notes, err := v.store.ListNotes(ctx, store.ListNotesOptions{TaskID: &t, IncludeArchived: true})
		if err != nil {
			continue
		}
		for _, n := range notes {
			allNoteIDs = append(allNoteIDs, n.ID)
		}
	}
	if len(allNoteIDs) == 0 {
		return nil
	}
	return v.embedNotes(ctx, allNoteIDs)
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

// chunkInput is one prepared chunk ready for embedding.
type chunkInput struct {
	Text       string
	ChunkIndex int
}

// buildTaskHeader builds the metadata header that prefixes each task chunk so
// mid-description chunks remain self-contained for retrieval.
func buildTaskHeader(t model.Task) string {
	var b strings.Builder
	b.WriteString("Title: ")
	b.WriteString(t.Title)
	if len(t.Tags) > 0 {
		tags := make([]string, len(t.Tags))
		for i, tt := range t.Tags {
			tags[i] = tt.Tag
		}
		b.WriteString(". Tags: ")
		b.WriteString(strings.Join(tags, ", "))
	}
	var linkDescs []string
	for _, l := range t.Links {
		if l.Description != "" {
			linkDescs = append(linkDescs, l.Description)
		}
	}
	if len(linkDescs) > 0 {
		b.WriteString(". Links: ")
		b.WriteString(strings.Join(linkDescs, "; "))
	}
	fmt.Fprintf(&b, ". Priority: %d. State: %s", t.Priority, t.State)

	header := b.String()
	if chunker.RuneCount(header) > headerMaxRunes {
		runes := []rune(header)
		header = string(runes[:headerMaxRunes-1]) + "…"
	}
	return header
}

// buildTaskChunks emits one or more chunks for a task. Empty descriptions
// produce a single header-only chunk so every task is searchable.
func buildTaskChunks(t model.Task) []chunkInput {
	header := buildTaskHeader(t)
	headerWithSep := header + ". "

	desc := strings.TrimSpace(model.DerefStr(t.Description))
	if desc == "" {
		return []chunkInput{{Text: header, ChunkIndex: 0}}
	}

	bodyBudget := chunkMaxRunes - chunker.RuneCount(headerWithSep)
	if bodyBudget < 100 {
		// Defensive — header cap should keep us well above this.
		bodyBudget = 100
	}
	bodyChunks := chunker.ChunkText(desc, bodyBudget, chunkOverlap)
	if len(bodyChunks) == 0 {
		return []chunkInput{{Text: header, ChunkIndex: 0}}
	}

	out := make([]chunkInput, len(bodyChunks))
	for i, body := range bodyChunks {
		out[i] = chunkInput{Text: headerWithSep + body, ChunkIndex: i}
	}
	return out
}

// buildNoteChunks emits chunks for a note. Notes always have non-empty text
// (validator enforces this) so we always emit ≥ 1 chunk.
func buildNoteChunks(n model.Note, parentTitle string) []chunkInput {
	var header string
	if parentTitle != "" {
		header = "Note for: " + parentTitle
		if chunker.RuneCount(header) > headerMaxRunes {
			runes := []rune(header)
			header = string(runes[:headerMaxRunes-1]) + "…"
		}
	} else {
		header = "Note"
	}
	headerWithSep := header + ". "

	bodyBudget := chunkMaxRunes - chunker.RuneCount(headerWithSep)
	if bodyBudget < 100 {
		bodyBudget = 100
	}
	bodyChunks := chunker.ChunkText(n.Text, bodyBudget, chunkOverlap)
	if len(bodyChunks) == 0 {
		// Validator forbids empty notes; this path is defensive.
		return []chunkInput{{Text: headerWithSep + n.Text, ChunkIndex: 0}}
	}

	out := make([]chunkInput, len(bodyChunks))
	for i, body := range bodyChunks {
		out[i] = chunkInput{Text: headerWithSep + body, ChunkIndex: i}
	}
	return out
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

	// Over-fetch chunks so per-doc aggregation has enough coverage to fill `limit` docs.
	results, err := v.vs.Search(ctx, vec, expandedLimit(limit), filter)
	if err != nil {
		return nil, err
	}

	return aggregateByDoc(results, limit), nil
}

func (v *VectorSyncer) SemanticSearchContext(ctx context.Context, taskID uint, opts store.SemanticSearchOptions) ([]store.SemanticSearchResult, error) {
	// Aggregate task text + all note texts into a single query
	detail, err := v.store.GetTask(ctx, taskID, store.GetTaskOptions{
		Include: map[string]bool{"description": true, "notes": true, "links": true},
	})
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString(detail.Title)
	if d := model.DerefStr(detail.Description); d != "" {
		b.WriteString(". ")
		b.WriteString(d)
	}
	for _, l := range detail.Links {
		if l.Description != "" {
			b.WriteString(". ")
			b.WriteString(l.Description)
		}
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

	// Exclude every chunk whose task_id matches the source task. This covers the
	// task's own chunks and its attached-note chunks (notes carry task_id metadata).
	tid := taskID
	filter := vectorstore.SearchFilter{
		ExcludeTaskID: &tid,
	}
	if opts.Type != "" {
		filter.Type = &opts.Type
	}
	if !opts.IncludeArchived {
		f := false
		filter.Archived = &f
	}

	results, err := v.vs.Search(ctx, vec, expandedLimit(limit), filter)
	if err != nil {
		return nil, err
	}

	return aggregateByDoc(results, limit), nil
}

// expandedLimit returns the chunk-level fetch size used to ensure per-doc
// aggregation has enough material to fill `limit` distinct docs.
func expandedLimit(limit int) int {
	n := limit * 5
	if n > 500 {
		n = 500
	}
	if n < limit {
		n = limit
	}
	return n
}

// aggregateByDoc groups chunk-level results by parent doc, then returns at most
// `limit` results sorted by best-chunk score.
func aggregateByDoc(results []vectorstore.SearchResult, limit int) []store.SemanticSearchResult {
	type bucket struct {
		out *store.SemanticSearchResult
	}
	buckets := make(map[string]*bucket)
	order := make([]string, 0)

	for _, r := range results {
		parentID := parentDocID(r)
		b, ok := buckets[parentID]
		if !ok {
			res := store.SemanticSearchResult{
				ID:       parentID,
				Text:     r.Text,
				Metadata: r.Metadata,
				Score:    r.Score,
			}
			b = &bucket{out: &res}
			buckets[parentID] = b
			order = append(order, parentID)
		} else if r.Score > b.out.Score {
			b.out.Score = r.Score
			b.out.Text = r.Text
			b.out.Metadata = r.Metadata
		}
		b.out.Chunks = append(b.out.Chunks, store.ChunkMatch{
			Text:       r.Text,
			Score:      r.Score,
			ChunkIndex: r.ChunkIndex,
		})
	}

	out := make([]store.SemanticSearchResult, 0, len(order))
	for _, id := range order {
		res := buckets[id].out
		sort.Slice(res.Chunks, func(i, j int) bool {
			return res.Chunks[i].Score > res.Chunks[j].Score
		})
		out = append(out, *res)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Score > out[j].Score
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// parentDocID returns the doc-level identifier ("task:42", "note:17") derived
// from chunk metadata.
func parentDocID(r vectorstore.SearchResult) string {
	docType, _ := r.Metadata["type"].(string)
	switch docType {
	case "task":
		if tid, ok := r.Metadata["task_id"].(int); ok {
			return fmt.Sprintf("task:%d", tid)
		}
	case "note":
		if nid, ok := r.Metadata["note_id"].(int); ok {
			return fmt.Sprintf("note:%d", nid)
		}
	}
	// Fallback: strip the trailing :chunkIndex from the row id, if present.
	if idx := strings.LastIndex(r.ID, ":"); idx > 0 {
		return r.ID[:idx]
	}
	return r.ID
}

// Reindex re-embeds all tasks and notes from the relational store into the vector store.
func (v *VectorSyncer) Reindex(ctx context.Context, clear bool, progressFn func(done, total int)) error {
	if clear {
		if err := v.vs.Reset(ctx, v.embedder.ModelName(), v.embedder.Dimensions()); err != nil {
			return fmt.Errorf("reset vector store: %w", err)
		}
	}

	// Fetch all tasks. Reindex embeds description + tags + links into chunks,
	// so opt description and links in here. Tags are always loaded by ListTasks.
	tasks, err := v.store.ListTasks(ctx, store.ListTasksOptions{
		IncludeArchived: true,
		IncludeSubtasks: true,
		Include:         map[string]bool{"description": true, "links": true},
	})
	if err != nil {
		return fmt.Errorf("listing tasks: %w", err)
	}

	// Build per-task lookups used during note embedding (archived flag for
	// metadata, title for chunk headers). One pass over tasks instead of
	// re-fetching per batch.
	taskArchived := make(map[uint]bool, len(tasks))
	taskTitle := make(map[uint]string, len(tasks))
	for _, t := range tasks {
		taskArchived[t.ID] = t.Archived
		taskTitle[t.ID] = t.Title
	}

	// Fetch all notes (attached + standalone), including archived so reindex
	// covers every note that may have a stale embedding.
	allNotes, err := v.store.ListNotes(ctx, store.ListNotesOptions{Scope: store.NoteScopeAll, IncludeArchived: true})
	if err != nil {
		return fmt.Errorf("listing notes: %w", err)
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
			// Without --clear we still need to drop any stale chunks from a prior
			// indexing; chunk counts may have shrunk.
			if !clear {
				if err := v.vs.DeleteTaskDocs(ctx, t.ID); err != nil {
					return fmt.Errorf("deleting prior task chunks: %w", err)
				}
			}
			chunks := buildTaskChunks(t.Task)
			for _, c := range chunks {
				texts = append(texts, c.Text)
				docs = append(docs, vectorstore.Document{
					ID:         fmt.Sprintf("task:%d:%d", t.ID, c.ChunkIndex),
					Text:       c.Text,
					ChunkIndex: c.ChunkIndex,
					Metadata: map[string]any{
						"type":     "task",
						"task_id":  int(t.ID),
						"state":    string(t.State),
						"priority": t.Priority,
						"archived": t.Archived,
					},
				})
			}
		}

		if len(docs) == 0 {
			done += len(batch)
			if progressFn != nil {
				progressFn(done, total)
			}
			continue
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

		for _, n := range batch {
			if !clear {
				if err := v.vs.DeleteNoteDocs(ctx, n.ID); err != nil {
					return fmt.Errorf("deleting prior note chunks: %w", err)
				}
			}
			meta := map[string]any{
				"type":    "note",
				"note_id": int(n.ID),
			}
			var parentTitle string
			if n.TaskID != nil {
				meta["task_id"] = int(*n.TaskID)
				if a, ok := taskArchived[*n.TaskID]; ok {
					meta["archived"] = a
					parentTitle = taskTitle[*n.TaskID]
				} else {
					// Orphan with stale task_id; treat as standalone.
					meta["archived"] = n.Archived
				}
			} else {
				meta["archived"] = n.Archived
			}

			chunks := buildNoteChunks(n, parentTitle)
			for _, c := range chunks {
				cmeta := make(map[string]any, len(meta))
				for k, v := range meta {
					cmeta[k] = v
				}
				texts = append(texts, c.Text)
				docs = append(docs, vectorstore.Document{
					ID:         fmt.Sprintf("note:%d:%d", n.ID, c.ChunkIndex),
					Text:       c.Text,
					ChunkIndex: c.ChunkIndex,
					Metadata:   cmeta,
				})
			}
		}

		if len(docs) == 0 {
			done += len(batch)
			if progressFn != nil {
				progressFn(done, total)
			}
			continue
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

// Compile-time interface checks.
var _ store.StoreObserver = (*VectorSyncer)(nil)
var _ store.SemanticSearcher = (*VectorSyncer)(nil)
