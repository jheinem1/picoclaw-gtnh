#!/usr/bin/env python3
import html
import json
import re
import sys
import urllib.parse

SOURCE = "wiki.gtnewhorizons.com/w/api.php"
STOP_WORDS = {
    "a", "all", "an", "and", "do", "for", "how", "in", "is", "me", "need",
    "of", "our", "requirements", "setup", "system", "the", "to", "we", "what", "with",
}


def clean(value):
    value = re.sub(r"<[^>]*>", "", value or "")
    return re.sub(r"\s+", " ", html.unescape(value)).strip()


def query_terms(query):
    return [
        word.lower()
        for word in re.findall(r"[A-Za-z0-9]+", query)
        if len(word) >= 3 and word.lower() not in STOP_WORDS
    ]


def focused_extract(value, terms, max_chars):
    if not value or not terms:
        return ""
    lower = value.lower()
    candidates = []
    phrase = " ".join(terms)
    if phrase:
        position = lower.find(phrase)
        if position >= 0:
            candidates.append((len(terms) + 2, position))
    for term in terms:
        for match in re.finditer(re.escape(term), lower):
            position = match.start()
            nearby = lower[max(0, position - 350):position + max_chars]
            score = sum(1 for candidate in set(terms) if candidate in nearby)
            candidates.append((score, position))
    if not candidates:
        return ""

    _, position = max(candidates, key=lambda item: (item[0], -item[1]))
    start = max(0, position - 300)
    end = min(len(value), start + max_chars)
    if start > 0:
        boundary = max(value.rfind("\n", 0, start), value.rfind(". ", 0, start))
        if boundary >= max(0, start - 250):
            start = boundary + 1
    if end < len(value):
        boundary = value.find(". ", end)
        if 0 <= boundary <= end + 250:
            end = boundary + 1
    return clean(value[start:end])[:max_chars]


def main():
    if len(sys.argv) != 6:
        raise SystemExit(
            "usage: wiki_search_enrich.py <query> <strategy> <max-chars> "
            "<search-json> <detail-json>"
        )
    query, strategy, max_chars_raw, search_path, detail_path = sys.argv[1:]
    max_chars = max(200, int(max_chars_raw))
    with open(search_path, "r", encoding="utf-8") as handle:
        search = json.load(handle)
    with open(detail_path, "r", encoding="utf-8") as handle:
        detail = json.load(handle)

    pages = {
        page.get("title", ""): page
        for page in detail.get("query", {}).get("pages", [])
        if page.get("title")
    }
    terms = query_terms(query)
    matches = []
    for result in search.get("query", {}).get("search", []):
        title = result.get("title", "")
        summary = focused_extract(pages.get(title, {}).get("extract", ""), terms, max_chars)
        summary_source = "extract_window" if summary else "search_snippet"
        if not summary:
            summary = clean(result.get("snippet", ""))
        matches.append({
            "title": title,
            "url": "https://wiki.gtnewhorizons.com/wiki/"
            + urllib.parse.quote(title.replace(" ", "_"), safe="_()"),
            "summary": summary,
            "summary_source": summary_source,
        })

    print(json.dumps({
        "ok": True,
        "exact": False,
        "query": query,
        "strategy": strategy,
        "matches": matches,
        "source": SOURCE,
    }, separators=(",", ":")))


if __name__ == "__main__":
    main()
