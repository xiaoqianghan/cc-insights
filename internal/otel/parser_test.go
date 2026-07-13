package otel

import "testing"

func TestNormalizeModelName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "Bedrock Opus",
			raw:  "us.anthropic.claude-opus-4-8",
			want: "opus-4-8",
		},
		{
			name: "versioned Bedrock Sonnet",
			raw:  "us.anthropic.claude-sonnet-4-5-20250115-v1:0",
			want: "sonnet-4-5",
		},
		{
			name: "Bedrock Fable",
			raw:  "us.anthropic.claude-fable-5",
			want: "fable-5",
		},
		{
			name: "Mythos",
			raw:  "claude-mythos-5",
			want: "mythos-5",
		},
		{
			name: "Sonnet 5",
			raw:  "claude-sonnet-5",
			want: "sonnet-5",
		},
		{
			name: "surrounding whitespace",
			raw:  "  claude-fable-5[1m]  ",
			want: "fable-5",
		},
		{
			name: "unknown model",
			raw:  "custom-model",
			want: "custom-model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeModelName(tt.raw); got != tt.want {
				t.Fatalf("NormalizeModelName(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
