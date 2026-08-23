package agent

import (
	"strings"
	"testing"
)

func TestBuildWrapperScriptIncludesPowerShellCompatibilityPrelude(t *testing.T) {
	script := buildWrapperScript(`C:\ProgramData\JikeAgent\script.ps1`, `C:\ProgramData\JikeAgent\out.txt`, `C:\ProgramData\JikeAgent\err.txt`)

	checks := []string{
		"Get-Command ConvertTo-Json",
		"System.Web.Script.Serialization.JavaScriptSerializer",
		"function ConvertTo-Json",
		"Out-File",
	}
	for _, check := range checks {
		if !strings.Contains(script, check) {
			t.Errorf("wrapper script does not contain %q", check)
		}
	}
}
