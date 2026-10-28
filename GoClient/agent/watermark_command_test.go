package agent

import (
	"strings"
	"testing"
	"time"
)

func TestIsWatermarkCommand(t *testing.T) {
	if !isWatermarkCommand(" \r\n" + WatermarkCommandPrefix + ` {"operation":"show"}`) {
		t.Fatal("leading whitespace must not hide the watermark prefix")
	}
	if isWatermarkCommand("Write-Output '" + WatermarkCommandPrefix + "'") {
		t.Fatal("the prefix inside a normal script must not be treated as a command")
	}
}

func TestParseWatermarkCommandDefaults(t *testing.T) {
	command, err := parseWatermarkCommand(WatermarkCommandPrefix + `{"text":"  内部资料  "}`)
	if err != nil {
		t.Fatalf("parse command: %v", err)
	}
	if command.Operation != "show" {
		t.Fatalf("an omitted operation must default to show, got %q", command.Operation)
	}
	if command.Text != "内部资料" {
		t.Fatalf("text must be trimmed, got %q", command.Text)
	}
	if command.Opacity != nil || command.FontSize != nil || command.Angle != nil {
		t.Fatalf("omitted numeric fields must stay nil so the client keeps its stored style: %+v", command)
	}
}

func TestParseWatermarkCommandNormalizesOperation(t *testing.T) {
	command, err := parseWatermarkCommand(WatermarkCommandPrefix + `{"operation":" UPDATE "}`)
	if err != nil {
		t.Fatalf("parse command: %v", err)
	}
	if command.Operation != "update" {
		t.Fatalf("operation must be trimmed and lower-cased, got %q", command.Operation)
	}
}

func TestParseWatermarkCommandReadsOptionalFields(t *testing.T) {
	command, err := parseWatermarkCommand(WatermarkCommandPrefix +
		`{"operation":"show","opacity":40,"color":"#000000","fontSize":18,"angle":-45,"spacingX":300,"spacingY":200}`)
	if err != nil {
		t.Fatalf("parse command: %v", err)
	}
	if command.Opacity == nil || *command.Opacity != 40 {
		t.Fatalf("unexpected opacity: %+v", command.Opacity)
	}
	if command.Angle == nil || *command.Angle != -45 {
		t.Fatalf("a negative angle must survive parsing: %+v", command.Angle)
	}
	if command.SpacingX == nil || *command.SpacingX != 300 || command.SpacingY == nil || *command.SpacingY != 200 {
		t.Fatalf("unexpected spacing: %+v %+v", command.SpacingX, command.SpacingY)
	}
	if command.Color != "#000000" {
		t.Fatalf("unexpected color: %q", command.Color)
	}
}

func TestParseWatermarkCommandRejectsBadInput(t *testing.T) {
	if _, err := parseWatermarkCommand(WatermarkCommandPrefix); err == nil {
		t.Fatal("an empty payload must fail")
	}
	if _, err := parseWatermarkCommand(WatermarkCommandPrefix + `{not json`); err == nil {
		t.Fatal("malformed JSON must fail")
	}

	oversize := WatermarkCommandPrefix + `{"operation":"show","text":"` +
		strings.Repeat("a", maxWatermarkCommandBytes) + `"}`
	if _, err := parseWatermarkCommand(oversize); err == nil {
		t.Fatal("an oversize payload must be rejected")
	}
}

func TestClampWatermarkValue(t *testing.T) {
	if got := clampWatermarkValue(-5, 0, 100); got != 0 {
		t.Fatalf("unexpected low clamp: %d", got)
	}
	if got := clampWatermarkValue(250, 0, 100); got != 100 {
		t.Fatalf("unexpected high clamp: %d", got)
	}
	if got := clampWatermarkValue(42, 0, 100); got != 42 {
		t.Fatalf("an in-range value must pass through: %d", got)
	}
}

func TestParseHexColor(t *testing.T) {
	cases := []struct {
		input string
		red   byte
		green byte
		blue  byte
	}{
		{"#FFFFFF", 0xFF, 0xFF, 0xFF},
		{"ffffff", 0xFF, 0xFF, 0xFF},
		{"#000000", 0x00, 0x00, 0x00},
		{"#1A2B3C", 0x1A, 0x2B, 0x3C},
		{"#fff", 0xFF, 0xFF, 0xFF},
		{"#12345", 0xFF, 0xFF, 0xFF},
		{"not-a-color", 0xFF, 0xFF, 0xFF},
		{"", 0xFF, 0xFF, 0xFF},
	}
	for _, testCase := range cases {
		red, green, blue := parseHexColor(testCase.input)
		if red != testCase.red || green != testCase.green || blue != testCase.blue {
			t.Fatalf("parseHexColor(%q) = %d,%d,%d; want %d,%d,%d",
				testCase.input, red, green, blue, testCase.red, testCase.green, testCase.blue)
		}
	}
}

func TestSubstituteWatermarkPlaceholders(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 5, 0, 0, time.UTC)
	got := substituteWatermarkPlaceholders("{UserName}@{SessionId} {DateTime}", `CORP\alice`, 2, now)
	if got != `CORP\alice@2 2026-10-27 09:05` {
		t.Fatalf("unexpected substitution result: %q", got)
	}

	untouched := substituteWatermarkPlaceholders("{HostName} 固定资产", `CORP\alice`, 2, now)
	if untouched != "{HostName} 固定资产" {
		t.Fatalf("server-side placeholders must be left for the server: %q", untouched)
	}
}

func TestDefaultWatermarkPolicy(t *testing.T) {
	policy := defaultWatermarkPolicy()
	if policy.Enabled || policy.Text != "" {
		t.Fatalf("a fresh policy must be disabled and empty: %+v", policy)
	}
	if policy.Opacity != defaultWatermarkOpacity || policy.Color != defaultWatermarkColor ||
		policy.FontSize != defaultWatermarkFontSize || policy.Angle != defaultWatermarkAngle {
		t.Fatalf("unexpected default style: %+v", policy)
	}
}
