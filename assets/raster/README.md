# Raster logo exports

SVG sources live in `../logo/`. Generate PNGs at common sizes when you have a
local SVG renderer (optional — not required for CI).

## Sizes

`16`, `32`, `64`, `128`, `256`, `512` → `shiftlock-{size}.png`

Prefer exporting from `shiftlock-mark.svg` (icon) for square rasters.

## PowerShell (Windows, with Inkscape)

```powershell
./export-rasters.ps1
```

## Manual (rsvg-convert / Inkscape / cairosvg)

```bash
for s in 16 32 64 128 256 512; do
  inkscape ../logo/shiftlock-mark.svg -w $s -h $s -o shiftlock-$s.png
done
```

If no renderer is installed, keep SVGs as the source of truth; social PNGs already
exist under `../social/`.
