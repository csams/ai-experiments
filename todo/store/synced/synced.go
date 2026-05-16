package synced

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

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

	// entityLocks serializes concurrent re-embed work for the same task
	// or note. Without it, two events firing in quick succession for the
	// same entity (e.g. UpdateTask × 2) could interleave their
	// GetTask → DeleteTaskDocs → Upsert phases, letting the older
	// write's Upsert overwrite the newer write's. Keyed by
	// "task:<id>" / "note:<id>"; lazily-allocated *sync.Mutex per key.
	entityLocks sync.Map

	// Reconciler lifecycle. StartReconciler is non-blocking and idempotent;
	// StopReconciler cancels the goroutine and waits for it to exit. Long-
	// lived processes (the MCP server) start the reconciler at boot;
	// short-lived CLI commands don't bother — dirty rows persist in the DB
	// and get picked up by the next MCP-server tick.
	reconcilerMu     sync.Mutex
	reconcilerCancel context.CancelFunc
	reconcilerDone   chan struct{}
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

// taskLockKey / noteLockKey build the entityLocks keys. The keyspace is
// shared via a string prefix so a task lock and a note lock with the same
// numeric ID don't collide.
func taskLockKey(id uint) string { return fmt.Sprintf("task:%d", id) }
func noteLockKey(id uint) string { return fmt.Sprintf("note:%d", id) }

// lockEntities acquires every key's mutex in sorted order (preventing
// deadlock between concurrent callers with overlapping key sets) and
// returns a release func that unlocks in reverse order. Duplicate keys
// in the input are deduplicated to avoid self-deadlock. The release func
// is safe to call exactly once via defer.
//
// Lazy *sync.Mutex allocation via sync.Map.LoadOrStore: under contention
// the LoadOrStore returns the existing mutex; on a fresh key both
// readers race on store and only one's allocation is kept. The wasted
// allocation in the loser is negligible for our scale.
func (v *VectorSyncer) lockEntities(keys []string) func() {
	if len(keys) == 0 {
		return func() {}
	}
	sorted := append(make([]string, 0, len(keys)), keys...)
	sort.Strings(sorted)
	// Dedup adjacent duplicates (post-sort).
	j := 0
	for i := 0; i < len(sorted); i++ {
		if j > 0 && sorted[j-1] == sorted[i] {
			continue
		}
		sorted[j] = sorted[i]
		j++
	}
	sorted = sorted[:j]

	unlocks := make([]func(), len(sorted))
	for i, k := range sorted {
		actual, _ := v.entityLocks.LoadOrStore(k, &sync.Mutex{})
		mu := actual.(*sync.Mutex)
		mu.Lock()
		unlocks[i] = mu.Unlock
	}
	return func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
}

// OnEvent handles store events and syncs to the vector store.
// This is best-effort: failures are logged but do not propagate. On
// failure, affected entities are marked vector_dirty so the reconciler
// can re-embed them later.
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
			"note_ids", event.NoteIDs,
			"error", err,
		)
		v.markDirty(ctx, event)
	}
}

func (v *VectorSyncer) syncTasks(ctx context.Context, event store.StoreEvent) error {
	switch event.Type {
	case "task.created", "task.updated", "task.state_changed",
		"task.blockers_added", "task.blockers_removed", "task.blockers_updated",
		"task.bulk_state_changed", "task.bulk_priority_changed":
		return v.embedTasks(ctx, event.TaskIDs)

	case "task.archived", "task.unarchived":
		// Re-embed task (updates archived metadata) and all its notes.
		// The two calls take different lock keyspaces (task: then note:)
		// and release between phases, leaving an eventual-consistency
		// window — a concurrent same-task event squeezing in between
		// could land a partial state. Acceptable for best-effort sync;
		// the next mutation re-embeds and converges.
		if err := v.embedTasks(ctx, event.TaskIDs); err != nil {
			return err
		}
		return v.reembedTaskNotes(ctx, event.TaskIDs)

	case "task.deleted":
		// Delete all chunks for each task. Note rows for these tasks are not
		// removed here — note.deleted events handle that path explicitly when
		// the caller passed delete_notes:true; otherwise notes are orphaned
		// and stay in the index under their own doc ids.
		// Hold the same per-task lock the embed paths use, so a slow
		// concurrent embedTasks for the same id can't Upsert chunks
		// AFTER our DeleteTaskDocs and resurrect a deleted task.
		keys := make([]string, len(event.TaskIDs))
		for i, tid := range event.TaskIDs {
			keys[i] = taskLockKey(tid)
		}
		unlock := v.lockEntities(keys)
		defer unlock()
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
		// Same lock-around-delete pattern as task.deleted: a concurrent
		// embedNotes for the same id must not be able to Upsert chunks
		// after our DeleteNoteDocs lands.
		keys := make([]string, len(event.NoteIDs))
		for i, nid := range event.NoteIDs {
			keys[i] = noteLockKey(nid)
		}
		unlock := v.lockEntities(keys)
		defer unlock()
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
	if len(taskIDs) == 0 {
		return nil
	}
	// Hold a per-task lock for the whole GetTask → DeleteTaskDocs →
	// EmbedBatch → Upsert window so a concurrent embedTasks for the
	// same task can't interleave and let an older write's Upsert
	// overwrite a newer one.
	keys := make([]string, len(taskIDs))
	for i, tid := range taskIDs {
		keys[i] = taskLockKey(tid)
	}
	unlock := v.lockEntities(keys)
	defer unlock()

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

	if err := v.vs.Upsert(ctx, docs); err != nil {
		return err
	}
	// Clear the dirty flag on every input ID. Tasks that were skipped
	// inside the GetTask loop (deleted mid-flight) match zero rows, so
	// the clear is a safe no-op for those.
	if err := v.store.ClearVectorDirty(ctx, taskIDs, nil); err != nil {
		// Non-fatal: the embed succeeded; the worst that happens is the
		// reconciler re-processes these IDs on the next tick.
		v.logger.Warn("clear task dirty flag after embed failed", "error", err)
	}
	return nil
}

func (v *VectorSyncer) embedNotes(ctx context.Context, noteIDs []uint) error {
	if len(noteIDs) == 0 {
		return nil
	}
	// Per-note serialization (see embedTasks for the rationale).
	keys := make([]string, len(noteIDs))
	for i, nid := range noteIDs {
		keys[i] = noteLockKey(nid)
	}
	unlock := v.lockEntities(keys)
	defer unlock()

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

	if err := v.vs.Upsert(ctx, docs); err != nil {
		return err
	}
	if err := v.store.ClearVectorDirty(ctx, nil, noteIDs); err != nil {
		v.logger.Warn("clear note dirty flag after embed failed", "error", err)
	}
	return nil
}

// reembedTaskNotes re-embeds all notes for the given tasks, updating
// their metadata.
//
// Note: the per-task ListNotes call here is implicitly capped at
// defaultQueryLimit (200) after PR-19's policy unification. A single
// task with > 200 notes would have its tail notes skipped during this
// post-archive metadata refresh. The cap is acceptable because:
//   - It's vanishingly rare for one task to carry hundreds of notes.
//   - The failure mode is "stale metadata on tail notes," not data
//     loss; the next per-note mutation re-embeds with current metadata.
//   - The operator escape hatch (`todo vector reindex`) covers it.
//
// If a real workload runs into the cap, page this loop via Limit/Offset
// the same way loadAllNotes does.
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

// markDirty flags the affected entities so the reconciler will re-embed
// them on a later tick. Skipped for *.deleted events: the entity is gone
// from the relational store, so marking a non-existent row is a no-op,
// and the vector chunks (if any survived the failed delete) are orphans
// that only `todo vector reindex` can clean up. We still log so operators
// know a manual reindex may be warranted in those rare cases.
func (v *VectorSyncer) markDirty(ctx context.Context, event store.StoreEvent) {
	if event.Type == "task.deleted" || event.Type == "note.deleted" {
		v.logger.Warn("vector delete failed; run 'todo vector reindex' to clean orphans",
			"event", event.Type,
			"task_ids", event.TaskIDs,
			"note_ids", event.NoteIDs,
		)
		return
	}
	if err := v.store.MarkVectorDirty(ctx, event.TaskIDs, event.NoteIDs); err != nil {
		// MarkVectorDirty failed too — typically because the same DB
		// outage that caused the sync failure is still in play. Log so
		// the operator knows a manual reindex may be needed once the DB
		// comes back.
		v.logger.Warn("could not mark dirty after vector sync failure; run 'todo vector reindex' to recover",
			"event", event.Type,
			"task_ids", event.TaskIDs,
			"note_ids", event.NoteIDs,
			"error", err,
		)
	}
}

// StartReconciler kicks off a background goroutine that periodically
// drains dirty entities and re-embeds them. Safe to call once per
// VectorSyncer lifetime — subsequent calls are no-ops while a reconciler
// is already running. Callers must invoke StopReconciler before tearing
// down the store; see cmd/mcp.go for the wiring.
//
// interval bounds how often the reconciler wakes. batchSize bounds the
// per-tick work; pass 0 for the default (100).
func (v *VectorSyncer) StartReconciler(parent context.Context, interval time.Duration, batchSize int) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	v.reconcilerMu.Lock()
	defer v.reconcilerMu.Unlock()
	if v.reconcilerCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	v.reconcilerCancel = cancel
	v.reconcilerDone = done
	// Pass `done` as a parameter rather than reading v.reconcilerDone
	// from the goroutine's defer — StopReconciler nils the field, and
	// closing a nil channel panics.
	go v.runReconciler(ctx, done, interval, batchSize)
	v.logger.Info("vector reconciler started", "interval", interval, "batch_size", batchSize)
}

// StopReconciler signals the reconciler to exit and waits up to ctx's
// deadline for the goroutine to return. Idempotent.
//
// The struct fields are NOT nilled until the goroutine has actually
// exited. A concurrent StartReconciler arriving while Stop is waiting
// will see non-nil reconcilerCancel and become a no-op, which is the
// correct behavior — restart-after-stop isn't a supported pattern, and
// allowing it would risk two goroutines briefly co-existing.
func (v *VectorSyncer) StopReconciler(ctx context.Context) {
	v.reconcilerMu.Lock()
	cancel := v.reconcilerCancel
	done := v.reconcilerDone
	v.reconcilerMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			v.logger.Warn("vector reconciler shutdown timed out", "error", ctx.Err())
			// Don't clear the fields below — the goroutine is still
			// running. A concurrent Start would observe non-nil cancel
			// and stay a no-op, which is the safer failure mode than
			// allowing a second goroutine to spawn.
			return
		}
	}
	// Goroutine has exited. Clear fields under the lock so any
	// future Start (after a clean shutdown) can spawn a fresh one.
	v.reconcilerMu.Lock()
	v.reconcilerCancel = nil
	v.reconcilerDone = nil
	v.reconcilerMu.Unlock()
}

func (v *VectorSyncer) runReconciler(ctx context.Context, done chan struct{}, interval time.Duration, batchSize int) {
	defer close(done)
	// Fire one tick immediately on startup so dirty rows accumulated
	// while the process was down get cleared promptly. Subsequent ticks
	// follow the interval.
	v.reconcileOnce(ctx, batchSize)
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			v.reconcileOnce(ctx, batchSize)
			timer.Reset(interval)
		}
	}
}

// reconcileOnce drains one batch of dirty entities through the normal
// embed paths. On success the dirty flag is cleared inside embedTasks /
// embedNotes. On failure the flag stays set, so the next tick retries.
func (v *VectorSyncer) reconcileOnce(ctx context.Context, batchSize int) {
	taskIDs, noteIDs, err := v.store.ListVectorDirty(ctx, batchSize)
	if err != nil {
		v.logger.Warn("reconciler list failed", "error", err)
		return
	}
	if len(taskIDs) == 0 && len(noteIDs) == 0 {
		return
	}
	v.logger.Info("vector reconciler processing batch", "tasks", len(taskIDs), "notes", len(noteIDs))
	if len(taskIDs) > 0 {
		if err := v.embedTasks(ctx, taskIDs); err != nil {
			v.logger.Warn("reconciler embed tasks failed", "task_ids", taskIDs, "error", err)
		}
	}
	if len(noteIDs) > 0 {
		if err := v.embedNotes(ctx, noteIDs); err != nil {
			v.logger.Warn("reconciler embed notes failed", "note_ids", noteIDs, "error", err)
		}
	}
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

	// Fetch all tasks. Reindex embeds description + tags + links into
	// chunks, so opt description and links in here. Tags are always
	// loaded by ListTasks. Paginate so a DB with > maxQueryLimit rows
	// reindexes completely (a single ListTasks call is capped).
	tasks, err := loadAllTasks(ctx, v.store)
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

	// Fetch all notes (attached + standalone), including archived so
	// reindex covers every note that may have a stale embedding. Same
	// pagination pattern as tasks.
	allNotes, err := loadAllNotes(ctx, v.store)
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

// reindexPageSize is the per-call Limit used to paginate ListTasks /
// ListNotes during Reindex. Matched to the store's maxQueryLimit so the
// number of round-trips stays small for large indexes while still
// honoring the per-call cap.
const reindexPageSize = 1000

// loadAllTasks paginates through ListTasks to gather every task,
// regardless of how many rows live in the database. Returns the
// accumulated slice; callers in this package build per-task lookup
// maps from it.
func loadAllTasks(ctx context.Context, s store.Store) ([]model.TaskListItem, error) {
	var all []model.TaskListItem
	offset := 0
	for {
		page, err := s.ListTasks(ctx, store.ListTasksOptions{
			IncludeArchived: true,
			IncludeSubtasks: true,
			Include:         map[string]bool{"description": true, "links": true},
			Limit:           reindexPageSize,
			Offset:          offset,
		})
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		offset += len(page)
		// Short page means the source ran out; no need to issue
		// another query just to confirm zero.
		if len(page) < reindexPageSize {
			break
		}
	}
	return all, nil
}

// loadAllNotes paginates through ListNotes for the reindex path.
// Identical shape to loadAllTasks; the two helpers stay separate
// because their Option types are distinct.
func loadAllNotes(ctx context.Context, s store.Store) ([]model.Note, error) {
	var all []model.Note
	offset := 0
	for {
		page, err := s.ListNotes(ctx, store.ListNotesOptions{
			Scope:           store.NoteScopeAll,
			IncludeArchived: true,
			Limit:           reindexPageSize,
			Offset:          offset,
		})
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		offset += len(page)
		if len(page) < reindexPageSize {
			break
		}
	}
	return all, nil
}

// Compile-time interface checks.
var _ store.StoreObserver = (*VectorSyncer)(nil)
var _ store.SemanticSearcher = (*VectorSyncer)(nil)
