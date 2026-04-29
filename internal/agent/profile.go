package agent

import "strings"

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
