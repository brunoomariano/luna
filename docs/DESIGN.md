# Design

How Luna's visual material looks, and why. Read this before drawing anything new — a
second diagram that invents its own palette costs more than the diagram was worth.

This is not part of the four-document suite ([architecture](architecture.md),
[invariants](invariants.md), [decisions](decisions.md), [lessons](lessons.md)), for the
same reason `CONTRIBUTING.md` is not: none of the four should hold it. It describes how
things are *drawn*, not how anything works, and it changes on a different clock.

## The source

Everything derives from the cover, `assets/imgs/luna_banner.png`: a fine-nib ink drawing
of a figure holding a torch among classical ruins, with astronomical arcs and constellation
lines threaded behind. Gold appears exactly twice — the crescent behind her head, and the
flame.

Three properties of that image are the whole system:

1. **Line, not fill.** Everything is drawn with a thin stroke. Nothing is a solid block of
   colour.
2. **One accent, used almost nowhere.** The drawing is monochrome except for two gold
   marks, and both are the thing the image is about.
3. **Structure in the margins.** The arcs and orbits sit behind and around the subject.
   They never cross what you are meant to read.

A diagram that keeps those three reads as part of the same object even if nothing else
matches. A diagram that breaks them looks bolted on however carefully the colours were
copied.

## Two grounds

The cover is light. The diagrams are dark. That is deliberate: the cover is a plate, and
the diagrams are read inside a README that most people open in a dark theme.

They are the *same drawing inverted*, not two different styles — ink on ivory becomes
hairline on ink, and the gold shifts to stay legible on its ground.

| | **Light** (the cover) | **Dark** (diagrams) |
|---|---|---|
| ground | `#F3F0E9` ivory | `#14150F` ink |
| primary line | `#666664` graphite | `#D8D4CB` bone |
| accent | `#B69D6E` muted gold | `#C9A227` gold |

**The gold is not the same value on both grounds, and that is the point.** On ivory it has
to recede — `#B69D6E` is desaturated so it reads as a warm highlight rather than a
label. On ink it has to carry across a dark field, so `#C9A227` is more saturated. Copying
one to the other ground produces either a shout or a mumble.

New material is **dark** unless it is a cover plate.

## The dark palette

Seven values, and each has one job. If a new element does not obviously belong to one of
these rows, that is a signal to reuse rather than to add an eighth.

| Token | Value | Where |
|---|---|---|
| ground | `#14150F` | the field, and nothing else |
| line | `#D8D4CB` | arrows, rules, anything the eye follows |
| bright | `#E6E2D8` | a heading — the name of a piece |
| body | `#B9B4A8` | code and commands the reader is meant to read |
| muted | `#9B968A` | prose that explains what is above it |
| dim | `#8A8578` | a caption, an aside, a flag's meaning |
| faint | `#6F6B60` `#6A675D` `#7D7A6F` | section labels, hairlines, box borders |
| **accent** | `#C9A227` | see below |

### The accent has a rule

**Gold marks the thing the drawing exists to say, and nothing else.** One idea per diagram.

- `who-calls.svg` — the exit code coming back. The whole point is that the agent asks and
  cannot argue with the answer.
- `inside.svg` — the left edge of each stage, and the final exit line. The point is that
  three pieces hand off in one direction and produce one verdict.
- `verbs.svg` — the exit codes again, plus a thin rule beside each verb.

If you find yourself using gold for a second idea in the same drawing, the drawing is
trying to say two things and should be two drawings.

Two more places gold is allowed, both borrowed from the cover: a **crescent arc** in a
margin, and one or two small **four-pointed stars**. They carry no meaning. They are there
because the cover has them.

## Line weight

| Weight | What |
|---|---|
| `0.55` | hairlines — orbits, arcs, the inner boxes that group something |
| `0.8` | the border of a stage box |
| `1.0` | arrows and rules the reader follows |
| `1.1` | anything gold |

Gold is a hair thicker than bone at the same nominal weight, because a saturated stroke on
a dark ground reads thinner than a light one. That is compensation, not emphasis.

Nothing is thicker than `1.1`. There is no bold in this system; emphasis comes from colour
and from position, never from weight.

## Type

Two families, and the split is semantic rather than decorative.

- **Serif** — Georgia, then `'Times New Roman'`, then `serif`. Names, prose, captions.
  It is the register of the cover.
- **Monospace** — `ui-monospace`, then Menlo, Consolas, `monospace`. Anything a person
  would type or that a machine printed: commands, flags, paths, exit codes, TOML.

The rule is worth stating because it is the fastest way to make a diagram wrong: prose set
in monospace reads as output, and a command set in serif reads as description.

### The scale

| Size | Role |
|---|---|
| `17` | the name of a stage (`contract`, `verify`, `ledger`) |
| `16` | the name of an actor (`you`, `your agent`, `luna`) |
| `14` | a verb (`luna check`) |
| `12.5` | body prose |
| `11.5` | notes, code, captions — the default |
| `10.5` | the section label at the top, tracked wide |

> Earlier drawings also used `11` and `12`. Those are drift; pick from this table.

**Letter-spacing** is used only twice: `2` on the tracked section label (`WHO CALLS IT`),
and `1.1`–`1.7` on stage and actor names. Everywhere else it is default.

## Composition

**A section label, tracked and dim, then a full-width rule.** Every diagram opens this way.
It is what makes three separate files read as one series.

```
WHO CALLS IT
─────────────────────────────────────────────────────
```

**Left column for identity, right column for explanation.** A stage's name and role sit at
the left; what it does sits at a consistent x. In the current drawings that split is at
`x=330` for a full-width layout and `x=230` inside a box.

**Hairline structure lives in the margins and never crosses a word.** The arcs are placed
so their visible portion falls outside the text block. When one intrudes, move the arc —
never fade the text.

**Boxes have no fill.** A group is enclosed by a hairline rectangle, or marked by a single
gold rule at its left edge. There are no filled panels, no shadows, no gradients, no
rounded corners.

**Arrows are thin, with a small open chevron head.** The head is a two-segment path, never
a filled triangle.

## Format

**SVG, hand-written, checked into `docs/assets/imgs/`.** Vector so it scales, text so it
diffs, and small — the three current diagrams are 19 KB together.

### Presentation attributes, never CSS

**GitHub's sanitizer strips `<style>` blocks and `style=` attributes from SVG.** A diagram
written with CSS classes renders as dark text on a background that also disappeared.

So every value goes on the element:

```xml
<!-- yes -->
<text x="24" y="30" fill="#e6e2d8" font-family="Georgia, serif" font-size="17">contract</text>

<!-- no: the sanitizer removes this and the text turns black -->
<style>.name { fill: #e6e2d8; }</style>
<text class="name">contract</text>
```

It is more verbose and there is no way around it. Before committing a new diagram:

```sh
rg 'class=|<style' docs/assets/imgs/*.svg     # must find nothing
python3 -c "import xml.etree.ElementTree as ET; ET.parse('<file>.svg')"
rsvg-convert -w 960 <file>.svg -o /tmp/check.png    # then look at it
```

The last step is not optional. Two defects in the current set — a font ligature eating a
`|` in `<file|->`, and an orbit crossing a heading — were invisible in the source and
obvious in the render.

### Accessibility

Every diagram carries `role="img"` and a `<title>` plus `<desc>`, referenced by
`aria-labelledby`. **The `<desc>` is a full prose account of what the diagram says**, not a
label: a reader who cannot see it should get the same content, not a note that a picture
exists.

## What not to do

- **Do not fill a shape with colour.** This system is drawn, not blocked out.
- **Do not add an eighth grey.** If a new element needs a value between two rows, it
  probably belongs to one of them.
- **Do not use gold for a second idea** in one drawing.
- **Do not use bold or a heavier stroke for emphasis.** Colour and position carry it.
- **Do not let a hairline cross text**, even faintly.
- **Do not reach for a rendering library.** These are written by hand; a generated diagram
  brings a layout engine's aesthetic, and then there are two systems.
- **Do not put a diagram where a sentence works.** The ASCII boxes these replaced were
  correct and unloved; what earned the redraw was that the README's first example implied
  the wrong caller. A drawing that only restates the paragraph above it is decoration.
