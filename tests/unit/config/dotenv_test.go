package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/config"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
)

func TestParseDotenvAccepts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data string
		key  string
		want string
	}{
		{"plain value", "A=one\n", "A", "one"},
		{"empty value is supplied", "A=\n", "A", ""},
		{"single quotes are literal", "A='a \\n $B'\n", "A", `a \n $B`},
		{"double quotes with escaped quote and backslash", `A="a \"b\" \\ c"` + "\n", "A", `a "b" \ c`},
		{"no interpolation", "A=$B${C}\n", "A", "$B${C}"},
		{"comment and blank lines are skipped", "# note\n\nA=1\n", "A", "1"},
		{"windows line endings", "A=1\r\nB=2\r\n", "B", "2"},
		{"byte order mark", "\xEF\xBB\xBFA=1\n", "A", "1"},
		{"no trailing newline", "A=1", "A", "1"},
		{"spaces around the equals sign", "A = 1\n", "A", "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, findings := config.ParseDotenv(".env", []byte(tt.data))
			if len(findings) > 0 {
				t.Fatalf("findings: %v", findings)
			}
			got, ok := f.Lookup(tt.key)
			if !ok || got.Value != tt.want {
				t.Fatalf("%s = %q (present %v), want %q", tt.key, got.Value, ok, tt.want)
			}
		})
	}
}

func TestParseDotenvRefusesAndNeverEchoesTheLine(t *testing.T) {
	t.Parallel()
	c := fixture.Canary
	tests := []struct {
		name     string
		data     string
		wantLine int
		wantKey  string // the key the message may name; "" when none
	}{
		{"no equals sign", "ok=1\n" + c + " just words\n", 2, ""},
		{"export prefix", "export API_TOKEN=" + c + "\n", 1, ""},
		{"invalid key", "1BAD=" + c + "\n", 1, ""},
		{"unterminated double quote", `API_TOKEN="` + c + "\n", 1, "API_TOKEN"},
		{"unterminated single quote", "API_TOKEN='" + c + "\n", 1, "API_TOKEN"},
		{"characters after the closing quote", `API_TOKEN="a"` + c + "\n", 1, "API_TOKEN"},
		{"unsupported escape", `API_TOKEN="` + c + `\n"` + "\n", 1, "API_TOKEN"},
		{"duplicate key", "API_TOKEN=a\nAPI_TOKEN=" + c + "\n", 2, "API_TOKEN"},
		{"invalid UTF-8", "API_TOKEN=\xff" + c + "\n", 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, findings := config.ParseDotenv(".env.local", []byte(tt.data))
			if len(findings) == 0 {
				t.Fatal("no findings, want a refusal")
			}
			if _, ok := f.Lookup("API_TOKEN"); ok {
				t.Fatal("a refused file still supplies keys")
			}
			first := findings[0]
			if first.Code != config.CodeDotenvInvalid {
				t.Fatalf("code = %s, want %s", first.Code, config.CodeDotenvInvalid)
			}
			if first.Location.File() != ".env.local" {
				t.Fatalf("file = %q, want .env.local", first.Location.File())
			}
			if line, _, ok := first.Location.Position(); !ok || line != tt.wantLine {
				t.Fatalf("line = %d (known %v), want %d", line, ok, tt.wantLine)
			}
			for _, f := range findings {
				text := fmt.Sprintf("%+v %s %s", f, f.Message, f.Location.Pointer())
				if strings.Contains(text, c) {
					t.Fatalf("finding echoes the canary: %s", text)
				}
			}
			if tt.wantKey != "" && !strings.Contains(first.Message, tt.wantKey) {
				t.Fatalf("message %q does not name key %s", first.Message, tt.wantKey)
			}
		})
	}
}

func TestParseDotenvBoundsFindings(t *testing.T) {
	t.Parallel()
	data := strings.Repeat("bad line\n", 500)
	_, findings := config.ParseDotenv(".env", []byte(data))
	if len(findings) == 0 || len(findings) > 20 {
		t.Fatalf("findings = %d, want 1..20", len(findings))
	}
}

func FuzzParseDotenv(f *testing.F) {
	for _, seed := range []string{"A=1\n", `A="x\"y"`, "A='", "export A=1", "\xff", "A=1\nA=2\n", "#c\n\n"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		file, findings := config.ParseDotenv(".env", data)
		if len(findings) > 0 {
			if _, ok := file.Lookup("A"); ok {
				t.Fatal("a refused file supplies keys")
			}
		}
	})
}
