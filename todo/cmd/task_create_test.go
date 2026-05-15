package cmd

import (
	"strings"
	"testing"

	"github.com/csams/todo/model"
)

func TestParseLinkFlags(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		want    []model.LinkInput
		wantErr string // substring; empty = expect success
	}{
		{
			name: "happy_path_full",
			in: []string{
				"type=pr,url=https://github.com/foo/bar/pull/1,desc=initial PR",
			},
			want: []model.LinkInput{
				{Type: model.LinkPR, URL: "https://github.com/foo/bar/pull/1", Description: "initial PR"},
			},
		},
		{
			name: "description_alias",
			in:   []string{"type=jira,url=https://x/y,description=long form"},
			want: []model.LinkInput{
				{Type: model.LinkJira, URL: "https://x/y", Description: "long form"},
			},
		},
		{
			name: "reordered_keys",
			in:   []string{"url=https://x,type=url"},
			want: []model.LinkInput{
				{Type: model.LinkURL, URL: "https://x"},
			},
		},
		{
			name: "whitespace_trimmed",
			in:   []string{"  type = pr , url = https://x  "},
			want: []model.LinkInput{
				{Type: model.LinkPR, URL: "https://x"},
			},
		},
		{
			name: "url_with_query_string",
			in:   []string{"type=url,url=https://example.com/path?a=b&c=d"},
			want: []model.LinkInput{
				{Type: model.LinkURL, URL: "https://example.com/path?a=b&c=d"},
			},
		},
		{
			name: "multiple_links",
			in: []string{
				"type=pr,url=https://x/1",
				"type=jira,url=https://y/2,desc=second",
			},
			want: []model.LinkInput{
				{Type: model.LinkPR, URL: "https://x/1"},
				{Type: model.LinkJira, URL: "https://y/2", Description: "second"},
			},
		},
		{
			name: "nil_input",
			in:   nil,
			want: nil,
		},

		// Error cases
		{name: "empty_value", in: []string{""}, wantErr: "empty --link"},
		{name: "whitespace_only", in: []string{"   "}, wantErr: "empty --link"},
		{name: "missing_url", in: []string{"type=pr"}, wantErr: "missing required key 'url'"},
		{name: "missing_type", in: []string{"url=https://x"}, wantErr: "missing required key 'type'"},
		{name: "unknown_key", in: []string{"type=pr,url=https://x,foo=bar"}, wantErr: `unknown key "foo"`},
		{name: "duplicate_key", in: []string{"type=pr,type=jira,url=https://x"}, wantErr: `duplicate key "type"`},
		{name: "segment_no_equals", in: []string{"type=pr,url"}, wantErr: "no '='"},
		{name: "trailing_comma", in: []string{"type=pr,url=https://x,"}, wantErr: "no '='"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseLinkFlags(tc.in)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len(got) = %d, want %d (got=%+v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
