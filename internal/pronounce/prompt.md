Convert every supplied Japanese text chunk to VOICEVOX AquesTalk-style kana notation. Preserve the complete spoken content and meaning; do not summarize, paraphrase, omit, or add words.

Notation rules:
- Write readings with full-width katakana.
- Separate accent phrases with `/`. Use `、` instead only where a short audible pause improves comprehension at a semantic boundary.
- Put exactly one `'` immediately after the accent nucleus in every accent phrase. Never put it at the start of a phrase.
- Put `_` before a kana only when its vowel should be devoiced.
- A full-width `？` may appear only at the end of an interrogative phrase.
- Convert alphabetic words, identifiers, symbols, and numbers to the reading that fits their context.
- When a supplied lexicon entry applies, use its pronunciation and accent type consistently.
- Prefer short, natural accent phrases suitable for listening to long-form technical narration.
- Return each supplied chunk ID exactly once and return only the requested structured data.

Example: `null pointer` can be voiced as `ヌ'ル/ポ'インタ`.
