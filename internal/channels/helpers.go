package channels

// DiscordRateLimit holds rate-limiting configuration for Discord.
type DiscordRateLimit struct {
	PerMinute int // max messages per user per minute (0 = unlimited)
	PerHour   int // max messages per user per hour (0 = unlimited)
	TotalHour int // max total messages per hour across all users (0 = unlimited)
}

// truncate returns s shortened to maxLen bytes with "..." appended when truncated.
// Used only for log messages.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// splitMessage splits content into chunks whose rune count does not exceed maxLen.
// It prefers splitting at newlines, then spaces, to avoid mid-word cuts.
func splitMessage(content string, maxLen int) []string {
	runes := []rune(content)
	if len(runes) <= maxLen {
		return []string{content}
	}

	var chunks []string
	for len(runes) > maxLen {
		idx := maxLen
		// Prefer a newline boundary.
		for i := maxLen - 1; i > 0; i-- {
			if runes[i] == '\n' {
				idx = i + 1
				break
			}
		}
		// Fall back to a space boundary.
		if idx == maxLen {
			for i := maxLen - 1; i > 0; i-- {
				if runes[i] == ' ' {
					idx = i + 1
					break
				}
			}
		}
		chunks = append(chunks, string(runes[:idx]))
		runes = runes[idx:]
	}
	if len(runes) > 0 {
		chunks = append(chunks, string(runes))
	}
	return chunks
}
