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

// getUint extracts a uint argument, returns 0 if missing or negative.
func getUint(req mcpgo.CallToolRequest, key string) uint {
	v := getInt(req, key)
	if v < 0 {
		return 0
	}
	return uint(v)
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
		if f, ok := v.(float64); ok && f >= 0 {
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
func getTime(req mcpgo.CallToolRequest, key string) (*time.Time, error) {
	s := getStr(req, key)
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, fmt.Errorf("invalid date %q (use YYYY-MM-DD): %w", s, err)
	}
	utc := t.UTC()
	return &utc, nil
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

// requireStr extracts a required string argument, returns error if missing or empty.
func requireStr(req mcpgo.CallToolRequest, key string) (string, error) {
	args := req.GetArguments()
	v, ok := args[key].(string)
	if !ok || v == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return v, nil
}

// requireUint extracts a required uint argument, returns error if missing or < 1.
func requireUint(req mcpgo.CallToolRequest, key string) (uint, error) {
	args := req.GetArguments()
	v, ok := args[key].(float64)
	if !ok || v < 1 {
		return 0, fmt.Errorf("%s is required and must be a positive integer", key)
	}
	return uint(v), nil
}

// requireUintSlice extracts a required non-empty []uint argument.
func requireUintSlice(req mcpgo.CallToolRequest, key string) ([]uint, error) {
	ids := getUintSlice(req, key)
	if len(ids) == 0 {
		return nil, fmt.Errorf("%s is required and must be a non-empty array", key)
	}
	return ids, nil
}

// requireStrSlice extracts a required non-empty []string argument.
func requireStrSlice(req mcpgo.CallToolRequest, key string) ([]string, error) {
	strs := getStrSlice(req, key)
	if len(strs) == 0 {
		return nil, fmt.Errorf("%s is required and must be a non-empty array", key)
	}
	return strs, nil
}

// getOptBool extracts a *bool argument, returns nil if missing.
func getOptBool(req mcpgo.CallToolRequest, key string) *bool {
	args := req.GetArguments()
	if v, ok := args[key].(bool); ok {
		return &v
	}
	return nil
}

// getOptInt extracts a *int argument, returns nil if missing.
func getOptInt(req mcpgo.CallToolRequest, key string) *int {
	args := req.GetArguments()
	if v, ok := args[key].(float64); ok {
		i := int(v)
		return &i
	}
	return nil
}

// toJSON marshals v to a pretty JSON string.
func toJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": "marshal failed: %s"}`, err.Error())
	}
	return string(b)
}
