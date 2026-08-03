# Export ShiftLock mark SVG to PNG rasters (requires Inkscape on PATH).
$ErrorActionPreference = "Stop"
$sizes = 16, 32, 64, 128, 256, 512
$src = Join-Path $PSScriptRoot "..\logo\shiftlock-mark.svg"
$inkscape = Get-Command inkscape -ErrorAction SilentlyContinue
if (-not $inkscape) {
  Write-Host "Inkscape not found on PATH. See README.md for alternatives."
  exit 0
}
foreach ($s in $sizes) {
  $out = Join-Path $PSScriptRoot "shiftlock-$s.png"
  & inkscape $src -w $s -h $s -o $out
  Write-Host "wrote $out"
}
