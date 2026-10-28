package agent

import (
	"strings"
	"testing"
)

func TestIsFlagToken(t *testing.T) {
	flags := []string{"-action", "--image", "-lock"}
	for _, flag := range flags {
		if !isFlagToken(flag) {
			t.Fatalf("%q must be treated as a flag", flag)
		}
	}

	values := []string{"-30", "-1.5", "--", "-", "0", "set", "", "-30 degrees", "-内部资料", "-spacingX2"}
	for _, value := range values {
		if isFlagToken(value) {
			t.Fatalf("%q must be treated as a value, not a flag", value)
		}
	}
}

func TestParseChildFlagsKeepsNegativeValues(t *testing.T) {
	values := parseChildFlags([]string{
		"-action", "set",
		"-angle", "-30",
		"-opacity", "28",
		"-lock",
	})

	if values["action"] != "set" {
		t.Fatalf("unexpected action: %q", values["action"])
	}
	if values["angle"] != "-30" {
		t.Fatalf("a negative number must stay a value, got %q", values["angle"])
	}
	if values["lock"] != "" {
		t.Fatalf("valueless flag should map to an empty string, got %q", values["lock"])
	}
}

func TestParseChildFlagsKeepsDashLeadingText(t *testing.T) {
	values := parseChildFlags([]string{"-text", "- 内部资料 -", "-opacity", "28"})
	if values["text"] != "- 内部资料 -" {
		t.Fatalf("a watermark text starting with a dash must stay a value, got %q", values["text"])
	}
	if flagInt(values, "opacity", 0) != 28 {
		t.Fatalf("unexpected opacity: %q", values["opacity"])
	}
}

func TestParseChildFlagsLastValueWins(t *testing.T) {
	values := parseChildFlags([]string{"-style", "2", "-style", "10"})
	if values["style"] != "10" {
		t.Fatalf("expected the last occurrence to win, got %q", values["style"])
	}
}

func TestFlagIntAndBool(t *testing.T) {
	values := map[string]string{"size": "42", "bad": "abc", "empty": "  ", "on": "1", "off": "0"}

	if got := flagInt(values, "size", 7); got != 42 {
		t.Fatalf("unexpected size: %d", got)
	}
	if got := flagInt(values, "bad", 7); got != 7 {
		t.Fatalf("an unparsable value must fall back, got %d", got)
	}
	if got := flagInt(values, "empty", 7); got != 7 {
		t.Fatalf("a blank value must fall back, got %d", got)
	}
	if got := flagInt(values, "missing", 7); got != 7 {
		t.Fatalf("a missing key must fall back, got %d", got)
	}
	if !flagBool(values, "on") {
		t.Fatal("1 must be true")
	}
	if flagBool(values, "off") || flagBool(values, "missing") {
		t.Fatal("only a literal 1 counts as true")
	}
}

func TestLogScriptSummaryOnlyTruncatesNativeCommands(t *testing.T) {
	if got := logScriptSummary("  Get-Process  "); got != "Get-Process" {
		t.Fatalf("plain scripts should only be trimmed, got %q", got)
	}

	longPlain := strings.Repeat("a", maxCommandLogRunes*2)
	if got := logScriptSummary(longPlain); got != longPlain {
		t.Fatal("plain scripts must not be truncated even when long")
	}

	for _, prefix := range []string{WallpaperCommandPrefix, WatermarkCommandPrefix} {
		summary := logScriptSummary(prefix + strings.Repeat("b", maxCommandLogRunes*2))
		if !strings.HasPrefix(summary, prefix) {
			t.Fatalf("the command prefix must survive truncation: %q", summary)
		}
		if !strings.Contains(summary, "已截断") {
			t.Fatalf("a truncated summary must say so: %q", summary)
		}
		if len([]rune(summary)) >= maxCommandLogRunes*2 {
			t.Fatalf("summary was not shortened: %d runes", len([]rune(summary)))
		}
	}
}

func TestExitUnsupportedMessage(t *testing.T) {
	if got := exitUnsupported("水印功能"); got != "水印功能仅支持 Windows 平台。" {
		t.Fatalf("unexpected message: %q", got)
	}
}
