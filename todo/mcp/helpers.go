package mcp

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/csams/todo/model"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// getStr extracts a string argument, returns "" if missing.
func getStr(req mcpgo.CallToolRequest, key string) string {
	args := req.GetArguments()
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

// getInt extracts an int argument, returns 0 if missing.
func getInt(req mcpgo.CallToolRequest, key string) int {
	args := req.GetArguments()
	if v, ok := args[key].(float64); ok {
		return int(v)
	}
	return 0
}

// getUint extracts a uint argument, returns 0 if missing.
func getUint(req mcpgo.CallToolRequest, key string) uint {
	return uint(getInt(req, key))
}

// getBool extracts a bool argument, returns false if missing.
func getBool(req mcpgo.CallToolRequest, key string) bool {
	args := req.GetArguments()
	if v, ok := args[key].(bool); ok {
		return v
	}
	return false
}

// getUintSlice extracts a []uint from a JSON array argument.
func getUintSlice(req mcpgo.CallToolRequest, key string) []uint {
	args := req.GetArguments()
	arr, ok := args[key].([]any)
	if !ok {
		return nil
	}
	ids := make([]uint, 0, len(arr))
	for _, v := range arr {
		if f, ok := v.(float64); ok {
			ids = append(ids, uint(f))
		}
	}
	return ids
}

// getStrSlice extracts a []string from a JSON array argument.
func getStrSlice(req mcpgo.CallToolRequest, key string) []string {
	args := req.GetArguments()
	arr, ok := args[key].([]any)
	if !ok {
		return nil
	}
	strs := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			strs = append(strs, s)
		}
	}
	return strs
}

// getTime extracts a time from a YYYY-MM-DD string argument.
func getTime(req mcpgo.CallToolRequest, key string) *time.Time {
	s := getStr(req, key)
	if s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	utc := t.UTC()
	return &utc
}

// getState extracts and validates a TaskState argument.
func getState(req mcpgo.CallToolRequest, key string) (model.TaskState, error) {
	s := getStr(req, key)
	if s == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	state := model.TaskState(s)
	if !model.ValidTaskStates[state] {
		return "", fmt.Errorf("invalid state %q (valid: New, Progressing, Blocked, Unblocked, Done)", s)
	}
	return state, nil
}

// toJSON marshals v to a pretty JSON string.
func toJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
