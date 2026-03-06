package utils

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

// WindowsDriverStackPatchScript increases driver.exe default stack reserve from
// 1MB to 8MB at container startup.
//
// We patch at runtime so already-published Windows browser images with older
// driver binaries keep working without rebuilding every historical image tag.
const WindowsDriverStackPatchScript = `
$f = "C:/tools/zebrunner/driver.exe"
$b = [IO.File]::ReadAllBytes($f)
$peOffset = [BitConverter]::ToInt32($b, 0x3C)
$magic = [BitConverter]::ToUInt16($b, $peOffset + 24)

if ($magic -eq 0x20B) {
  $stackReserveOffset = $peOffset + 24 + 72
  $stackValueSize = 8
} elseif ($magic -eq 0x10B) {
  $stackReserveOffset = $peOffset + 24 + 68
  $stackValueSize = 4
} else {
  throw "Unsupported PE magic: $magic"
}

$oldStackReserve = if ($stackValueSize -eq 8) {
  [BitConverter]::ToInt64($b, $stackReserveOffset)
} else {
  [BitConverter]::ToInt32($b, $stackReserveOffset)
}

$newStackBytes = [byte[]]::new($stackValueSize)
[Array]::Copy([BitConverter]::GetBytes([long]8MB), 0, $newStackBytes, 0, $stackValueSize)
[Array]::Copy($newStackBytes, 0, $b, $stackReserveOffset, $stackValueSize)
[IO.File]::WriteAllBytes($f, $b)

Write-Host "Stack:$oldStackReserve->8MB"
`

// PowerShell expects -EncodedCommand payloads in Base64-encoded UTF-16LE.
// We must use -EncodedCommand because ECS/Docker on Windows strips bare `$`
// characters from container override commands before PowerShell receives them.
func BuildWindowsPowerShellEncodedCommand(script string) string {
	encodedScript := utf16.Encode([]rune(strings.TrimSpace(script)))
	encodedBytes := make([]byte, len(encodedScript)*2)

	for i, r := range encodedScript {
		binary.LittleEndian.PutUint16(encodedBytes[i*2:], r)
	}

	return "powershell -NoProfile -EncodedCommand " + base64.StdEncoding.EncodeToString(encodedBytes)
}
