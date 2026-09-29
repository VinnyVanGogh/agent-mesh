package security

import (
	"strings"
	"testing"
)

func TestSanitizeEnvDropsSecrets(t *testing.T) {
	env := []string{
		"PATH=/usr/bin", "HOME=/Users/x", "LANG=en_US.UTF-8", "LC_ALL=C", "GIT_AUTHOR_NAME=Ann",
		"AWS_SECRET_ACCESS_KEY=abc", "AWS_ACCESS_KEY_ID=AKIAXXXX", "GITHUB_TOKEN=ghp_x",
		"ANTHROPIC_API_KEY=sk-ant-x", "OPENAI_API_KEY=sk-x", "GOOGLE_APPLICATION_CREDENTIALS=/k.json",
		"SSH_AUTH_SOCK=/tmp/agent", "GIT_ASKPASS_TOKEN=zzz", "PAPERCLIP_API_KEY=pk", "RANDOM_THING=1",
		"malformed",
	}
	got := strings.Join(SanitizeEnv(env), "\n")
	for _, keep := range []string{"PATH=", "HOME=", "LANG=", "LC_ALL=", "GIT_AUTHOR_NAME="} {
		if !strings.Contains(got, keep) {
			t.Errorf("dropped %s", keep)
		}
	}
	for _, drop := range []string{"AWS_", "GITHUB_TOKEN", "ANTHROPIC", "OPENAI", "GOOGLE_APP", "SSH_AUTH_SOCK", "GIT_ASKPASS_TOKEN", "PAPERCLIP", "RANDOM_THING", "malformed"} {
		if strings.Contains(got, drop) {
			t.Errorf("leaked %s", drop)
		}
	}
}

func TestSanitizeEnvExtraPassthrough(t *testing.T) {
	got := SanitizeEnv([]string{"PATH=/bin", "ANTHROPIC_API_KEY=k", "GITHUB_TOKEN=t"}, "ANTHROPIC_API_KEY")
	if strings.Join(got, ",") != "PATH=/bin,ANTHROPIC_API_KEY=k" {
		t.Fatalf("got %v", got)
	}
}

func TestChildEnvNeverContainsUnlistedSecret(t *testing.T) {
	t.Setenv("STAYPOINT_TEST_MASTER_TOKEN", "x")
	for _, kv := range ChildEnv() {
		if strings.HasPrefix(kv, "STAYPOINT_TEST_MASTER_TOKEN=") {
			t.Fatal("leaked")
		}
	}
}
