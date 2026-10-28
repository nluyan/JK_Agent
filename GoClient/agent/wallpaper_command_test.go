package agent

import (
	"strings"
	"testing"
)

func TestIsWallpaperCommand(t *testing.T) {
	if !isWallpaperCommand(" \r\n" + WallpaperCommandPrefix + ` {"operation":"set"}`) {
		t.Fatal("leading whitespace must not hide the wallpaper prefix")
	}
	if isWallpaperCommand("Write-Output '" + WallpaperCommandPrefix + "'") {
		t.Fatal("the prefix inside a normal script must not be treated as a command")
	}
}

func TestParseWallpaperCommandDefaults(t *testing.T) {
	command, err := parseWallpaperCommand(WallpaperCommandPrefix + `{"imageUrl":"http://server/bg.jpg"}`)
	if err != nil {
		t.Fatalf("parse command: %v", err)
	}
	if command.Operation != "set" {
		t.Fatalf("an omitted operation must default to set, got %q", command.Operation)
	}
	if command.wallpaperStyle() != 10 {
		t.Fatalf("an omitted style must default to fill(10), got %d", command.wallpaperStyle())
	}
	if !command.wallpaperLock() {
		t.Fatal("an omitted lock must default to locked so users cannot revert the wallpaper")
	}
}

func TestParseWallpaperCommandExplicitValues(t *testing.T) {
	command, err := parseWallpaperCommand(WallpaperCommandPrefix +
		`{"operation":" CLEAR ","style":2,"tile":true,"lock":false,"fileName":"a/b.png"}`)
	if err != nil {
		t.Fatalf("parse command: %v", err)
	}
	if command.Operation != "clear" {
		t.Fatalf("operation must be trimmed and lower-cased, got %q", command.Operation)
	}
	if command.wallpaperStyle() != 2 {
		t.Fatalf("unexpected style: %d", command.wallpaperStyle())
	}
	if !command.Tile {
		t.Fatal("tile must be honoured")
	}
	if command.wallpaperLock() {
		t.Fatal("an explicit lock=false must not be overridden by the default")
	}
	if command.FileName != "a/b.png" {
		t.Fatalf("unexpected file name: %q", command.FileName)
	}
}

func TestParseWallpaperCommandRejectsBadInput(t *testing.T) {
	if _, err := parseWallpaperCommand(WallpaperCommandPrefix); err == nil {
		t.Fatal("an empty payload must fail")
	}
	if _, err := parseWallpaperCommand(WallpaperCommandPrefix + `{not json`); err == nil {
		t.Fatal("malformed JSON must fail")
	}

	oversize := WallpaperCommandPrefix + `{"operation":"set","imageBase64":"` +
		strings.Repeat("a", maxWallpaperCommandBytes) + `"}`
	if _, err := parseWallpaperCommand(oversize); err == nil {
		t.Fatal("an oversize payload must be rejected")
	}
}

func TestSafeWallpaperFileName(t *testing.T) {
	cases := map[string]string{
		"":                  defaultWallpaperFileName,
		"   ":               defaultWallpaperFileName,
		"../../evil.jpg":    "evil.jpg",
		"a/b/c.png":         "c.png",
		".hidden":           "hidden",
		"....":              defaultWallpaperFileName,
		`bad:name*?.png`:    "bad_name__.png",
		`pipe<>|"quote.png`: `pipe____quote.png`,
	}
	for input, expected := range cases {
		if got := safeWallpaperFileName(input); got != expected {
			t.Fatalf("safeWallpaperFileName(%q) = %q; want %q", input, got, expected)
		}
	}

	for _, input := range []string{"../../evil.jpg", "a/b/c.png", ".hidden"} {
		got := safeWallpaperFileName(input)
		if strings.ContainsAny(got, `/\`) || got == "." || got == ".." {
			t.Fatalf("a cleaned file name must stay inside the assets directory: %q -> %q", input, got)
		}
	}
}

func TestNormalizeBase64(t *testing.T) {
	if got := normalizeBase64("data:image/png;base64,AQID"); got != "AQID" {
		t.Fatalf("a data URI prefix must be stripped, got %q", got)
	}
	if got := normalizeBase64("AQ\r\nID\t"); got != "AQID" {
		t.Fatalf("line breaks and tabs must be stripped, got %q", got)
	}
	if got := normalizeBase64("AQ ID"); got != "AQID" {
		t.Fatalf("spaces must be stripped, got %q", got)
	}
	if got := normalizeBase64("AQID"); got != "AQID" {
		t.Fatalf("plain base64 must pass through, got %q", got)
	}
}

func TestResolveWallpaperImageRequiresSource(t *testing.T) {
	if _, err := resolveWallpaperImage(&wallpaperCommand{Operation: "set"}); err == nil {
		t.Fatal("a set command without imageBase64 or imageUrl must fail")
	}
}

func TestResolveWallpaperImageDecodesBase64(t *testing.T) {
	image, err := resolveWallpaperImage(&wallpaperCommand{
		Operation:   "set",
		ImageBase64: "data:image/png;base64,AQID",
	})
	if err != nil {
		t.Fatalf("resolve image: %v", err)
	}
	if len(image) != 3 {
		t.Fatalf("unexpected decoded length: %d", len(image))
	}

	if _, err := resolveWallpaperImage(&wallpaperCommand{ImageBase64: "!!!not base64!!!"}); err == nil {
		t.Fatal("invalid base64 must fail")
	}
}

func TestBoolFlag(t *testing.T) {
	if boolFlag(true) != "1" || boolFlag(false) != "0" {
		t.Fatal("boolFlag must produce the 0/1 form used on the child command line")
	}
}
