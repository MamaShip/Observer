#!/usr/bin/env python3
"""Subset 得意黑 Smiley Sans to the characters the web app's own text uses.

The display face is only for our headings (plan.md §10), so the full font
(fonts/SmileySans-Oblique.woff2, about 1.1 MB, not bundled) is cut down to
the characters found in src/ and index.html, plus printable ASCII and common
CJK punctuation. A heading whose text comes from elsewhere (a custom service
name) falls back glyph by glyph to the system font.

Run `make web-fonts` after changing interface text; tests/font.test.ts fails
when src/ has a character the subset was not made for. Needs fonttools and
brotli (pip install fonttools brotli).
"""
import pathlib
import sys

from fontTools import subset

web = pathlib.Path(__file__).resolve().parent.parent
src = web / "fonts" / "SmileySans-Oblique.woff2"
out = web / "src" / "assets" / "fonts" / "SmileySans-Oblique.woff2"
chars_file = web / "src" / "assets" / "fonts" / "SmileySans-Oblique.chars.txt"


def used_chars() -> set[str]:
    chars = {chr(c) for c in range(0x20, 0x7F)}
    chars |= set("，。、：；！？「」『』（）《》〈〉【】…—·～“”‘’　")
    files = [web / "index.html", *sorted((web / "src").rglob("*"))]
    for f in files:
        if f.suffix in {".vue", ".ts", ".html", ".css"} and f.is_file():
            chars |= {c for c in f.read_text(encoding="utf-8") if c.isprintable() and ord(c) > 0x7E}
    return chars


def main() -> int:
    chars = used_chars()
    opts = subset.Options()
    opts.flavor = "woff2"
    opts.layout_features = ["*"]
    opts.name_IDs = ["*"]
    opts.name_languages = ["*"]
    opts.notdef_outline = True
    font = subset.load_font(str(src), opts)
    cmap = font.getBestCmap()
    keep = sorted(c for c in chars if ord(c) in cmap)
    sub = subset.Subsetter(opts)
    sub.populate(unicodes=[ord(c) for c in keep])
    sub.subset(font)
    subset.save_font(font, str(out), opts)
    # Every character asked for, including those the font does not have, so
    # the test can tell a stale subset from a missing glyph.
    chars_file.write_text("".join(sorted(chars)) + "\n", encoding="utf-8")
    print(f"{len(keep)} characters, {out.stat().st_size // 1024} KB → {out.relative_to(web)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
