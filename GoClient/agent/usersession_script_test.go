package agent

import (
	"strings"
	"testing"
)

func TestParseUserSessionDirectiveDefaults(t *testing.T) {
	request, ok := parseUserSessionDirective(UserSessionDirective + "\nWrite-Output hello")
	if !ok {
		t.Fatal("directive on the first line must be recognized")
	}
	if request.AsSystem || request.NoWait || request.HasSession {
		t.Fatalf("unexpected flags: %+v", request)
	}
	if strings.TrimSpace(request.Script) != "Write-Output hello" {
		t.Fatalf("unexpected script body: %q", request.Script)
	}
}

func TestParseUserSessionDirectiveFlags(t *testing.T) {
	request, ok := parseUserSessionDirective("#jk-usersession -System -NoWait -Session 3\r\nexit 0\r\nexit 1")
	if !ok {
		t.Fatal("lowercase directive with flags must be recognized")
	}
	if !request.AsSystem || !request.NoWait {
		t.Fatalf("expected -System and -NoWait to be set: %+v", request)
	}
	if !request.HasSession || request.SessionID != 3 {
		t.Fatalf("expected explicit session 3: %+v", request)
	}
	if request.Script != "exit 0\nexit 1" {
		t.Fatalf("multi-line script body was not preserved: %q", request.Script)
	}
}

func TestParseUserSessionDirectiveToleratesBomAndBlankLines(t *testing.T) {
	withBom, ok := parseUserSessionDirective("\ufeff" + UserSessionDirective + " -System\r\nGet-Process")
	if !ok || !withBom.AsSystem {
		t.Fatalf("a UTF-8 BOM before the directive must be tolerated: ok=%v request=%+v", ok, withBom)
	}

	withBlankLines, ok := parseUserSessionDirective("\r\n   \r\n" + UserSessionDirective + "\r\nGet-Process")
	if !ok {
		t.Fatal("leading blank lines must be tolerated")
	}
	if strings.TrimSpace(withBlankLines.Script) != "Get-Process" {
		t.Fatalf("unexpected script body: %q", withBlankLines.Script)
	}
}

func TestParseUserSessionDirectiveIgnoresLookalikes(t *testing.T) {
	scripts := []string{
		UserSessionDirective + "X\nWrite-Output 1",
		UserSessionDirective + " -Unknown\nWrite-Output 1",
		UserSessionDirective + " -Session\nWrite-Output 1",
		UserSessionDirective + " -Session abc\nWrite-Output 1",
		UserSessionDirective,
		UserSessionDirective + "\n   \n",
		"Write-Output '" + UserSessionDirective + "'",
		"# 普通注释\nWrite-Output 1",
	}
	for _, script := range scripts {
		if _, ok := parseUserSessionDirective(script); ok {
			t.Fatalf("must not be treated as a user-session command: %q", script)
		}
		if isUserSessionCommand(script) {
			t.Fatalf("isUserSessionCommand disagreed with the parser: %q", script)
		}
	}
}

func TestIsUserSessionCommandAllowsLeadingWhitespace(t *testing.T) {
	if !isUserSessionCommand(" \r\n" + UserSessionDirective + "\nWrite-Output 1") {
		t.Fatal("leading whitespace must not hide the directive")
	}
}
