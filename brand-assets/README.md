# Athena Brand Assets

Vector brand assets for **Athena**, traced from the master memory-loop mark and
built into the same system used across Prescott Data projects.

## Files

`svg/` contains three lockups, each in five variants:

| Type | Description |
|------|-------------|
| `monogram-*` | The memory-loop mark on its own (favicon, app icon, avatar) |
| `wordmark-*` | The "Athena" logotype on its own |
| `combo-*` | Mark + logotype lockup (primary logo) |

| Variant | Use |
|---------|-----|
| `*-brand` | Brand blue `#0071F7` on light backgrounds |
| `*-black` | Ink navy `#0B1220` for monochrome light backgrounds |
| `*-white` | White `#FFFFFF` for dark backgrounds |
| `*-purple` | Accent purple `#4951F3` |
| `*-trimmed` | Ink navy, tightly cropped bounding box (for precise layout) |

## Palette

| Token | Hex |
|-------|-----|
| Brand | `#0071F7` |
| Ink | `#0B1220` |
| Purple | `#4951F3` |
| White | `#FFFFFF` |

## Notes

- The mark is a single traced path (potrace), so every asset is resolution
  independent and renders crisply at any size.
- The wordmark is set in Avenir Next Demi Bold, outlined to paths.
- To recolor, edit the `fill` attribute on the `<g>` element.
