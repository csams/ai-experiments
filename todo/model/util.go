package model

// DerefStr returns the value pointed to by p, or "" if p is nil.
func DerefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// PtrIfNonEmpty returns &s if s is non-empty, nil otherwise.
func PtrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// TaskIncludes is the full set of opt-in fields for get_task.
var TaskIncludes = []string{
	"description", "notes", "links", "parent", "children", "blockers", "blocking",
}

// TaskListIncludes is the opt-in set for list_tasks. Does not include
// "blocking" — list_tasks does not load that relation.
var TaskListIncludes = []string{
	"description", "notes", "links", "parent", "children", "blockers",
}

// AllTaskIncludesSet returns TaskIncludes as a set, for internal callers that
// want the legacy full payload.
func AllTaskIncludesSet() map[string]bool {
	set := make(map[string]bool, len(TaskIncludes))
	for _, k := range TaskIncludes {
		set[k] = true
	}
	return set
}
