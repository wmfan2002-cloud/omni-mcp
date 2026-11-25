package agent

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestPowershellArgsSuppressesCLIXMLProgressAndUsesUTF8(t *testing.T) {
	args := powershellArgs("powershell.exe", "Write-Output ok")
	if len(args) < 6 || args[4] != "-EncodedCommand" {
		t.Fatalf("unexpected powershell args: %#v", args)
	}
	raw, err := base64.StdEncoding.DecodeString(args[5])
	if err != nil {
		t.Fatalf("decode EncodedCommand: %v", err)
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(raw[i*2 : i*2+2])
	}
	decoded := string(utf16.Decode(units))
	for _, want := range []string{
		"$ProgressPreference='SilentlyContinue'",
		"[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new()",
		"$OutputEncoding=[System.Text.UTF8Encoding]::new()",
		"Write-Output ok",
	} {
		if !strings.Contains(decoded, want) {
			t.Fatalf("decoded PowerShell script missing %q: %s", want, decoded)
		}
	}
}
