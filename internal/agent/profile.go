package agent

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type Profile struct {
	Channel      Channel
	Instructions string
	ASCIIOnly    bool
	Markdown     bool
}

func ProfileForChannel(channel Channel) Profile {
	switch channel {
	case ChannelDiscord:
		return Profile{
			Channel: ChannelDiscord,
			Instructions: strings.Join([]string{
				"You are GregGPT for a GTNH Discord channel.",
				"Reply concisely.",
				"Markdown is allowed when it improves scanability.",
				"If a Discord response will invoke one or more tools, you must first emit one brief user-facing commentary message describing what you are about to check.",
				"This requirement also applies to simple lookups; one commentary message may cover a batch of parallel tool calls.",
				"For multi-step tasks, emit another brief commentary update when starting a materially distinct next step or after a material finding.",
				"If the user sends a steering update while you are working, incorporate it into the active task and first emit a brief commentary acknowledging how you are adjusting.",
				"Do not emit commentary for a direct answer that uses no tools, expose private reasoning, or dump raw tool output.",
				"In every final reply, explicitly name the Discord sender and briefly restate the question as you interpreted it before giving the answer.",
			}, "\n"),
			Markdown: true,
		}
	case ChannelMinecraft:
		fallthrough
	default:
		return Profile{
			Channel: ChannelMinecraft,
			Instructions: strings.Join([]string{
				"You are GregGPT for Minecraft chat in a GTNH server.",
				"Reply concisely.",
				"Use ASCII only.",
				"Do not use markdown.",
				"If a Minecraft response will invoke one or more tools, you must first emit one brief user-facing commentary message describing what you are about to check.",
				"Each commentary update must be exactly one brief ASCII plain-text sentence that fits within 180 characters.",
				"For a multi-step task, emit another one-sentence commentary update only when starting a materially distinct next step or after a material finding.",
				"If the player sends a steering update while you are working, incorporate it into the active task and first emit a one-sentence commentary acknowledging how you are adjusting.",
				"Do not emit commentary for a direct answer that uses no tools, expose private reasoning, or dump raw tool output.",
				"Answer directly without an '<player> - asking ...' preamble or a routine restatement of the question.",
				"Treat clearly fictional Minecraft or factory roleplay as fiction. Use real-world emergency guidance only when the player clearly indicates real-world danger; if that distinction is genuinely unclear, ask one brief clarifying question.",
			}, "\n"),
			ASCIIOnly: true,
			Markdown:  false,
		}
	}
}

func (p Profile) formatFinal(text string) string {
	text = strings.TrimSpace(text)
	if p.ASCIIOnly {
		text = asciiOnly(text)
	}
	if !p.Markdown {
		text = stripSimpleMarkdown(text)
	}
	return strings.TrimSpace(text)
}

func renderURLCitations(text string, citations []URLCitation, markdown bool) string {
	if len(citations) == 0 {
		return text
	}
	runes := []rune(text)
	valid := make([]URLCitation, 0, len(citations))
	for _, citation := range citations {
		if citation.StartIndex < 0 || citation.EndIndex <= citation.StartIndex || citation.EndIndex > len(runes) || !isSafeWebURL(citation.URL) {
			continue
		}
		valid = append(valid, citation)
	}
	if len(valid) == 0 {
		return text
	}
	sort.SliceStable(valid, func(i, j int) bool {
		if valid[i].StartIndex != valid[j].StartIndex {
			return valid[i].StartIndex < valid[j].StartIndex
		}
		if valid[i].EndIndex != valid[j].EndIndex {
			return valid[i].EndIndex < valid[j].EndIndex
		}
		return valid[i].URL < valid[j].URL
	})

	var b strings.Builder
	cursor := 0
	sourceNumbers := map[string]int{}
	for i := 0; i < len(valid); {
		citation := valid[i]
		if citation.StartIndex < cursor {
			i++
			continue
		}
		b.WriteString(string(runes[cursor:citation.StartIndex]))
		j := i
		groupURLs := map[string]bool{}
		for j < len(valid) && valid[j].StartIndex == citation.StartIndex && valid[j].EndIndex == citation.EndIndex {
			current := valid[j]
			if markdown && !groupURLs[current.URL] {
				number, ok := sourceNumbers[current.URL]
				if !ok {
					number = len(sourceNumbers) + 1
					sourceNumbers[current.URL] = number
				}
				b.WriteString("[[")
				b.WriteString(strconv.Itoa(number))
				b.WriteString("]](<")
				b.WriteString(strings.ReplaceAll(current.URL, ">", "%3E"))
				b.WriteString(">)")
				groupURLs[current.URL] = true
			}
			j++
		}
		cursor = citation.EndIndex
		i = j
	}
	b.WriteString(string(runes[cursor:]))
	return b.String()
}

func isSafeWebURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func asciiOnly(text string) string {
	replacements := map[rune]string{
		'“': `"`,
		'”': `"`,
		'‘': "'",
		'’': "'",
		'–': "-",
		'—': "-",
		'…': "...",
	}

	var b strings.Builder
	for _, r := range text {
		if r < 128 {
			b.WriteRune(r)
			continue
		}
		if replacement, ok := replacements[r]; ok {
			b.WriteString(replacement)
		}
	}
	return b.String()
}

func stripSimpleMarkdown(text string) string {
	replacer := strings.NewReplacer(
		"```", "",
		"`", "",
		"**", "",
		"__", "",
		"~~", "",
	)
	lines := strings.Split(replacer.Replace(text), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimLeft(line, "#>")
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, "\n")
}
