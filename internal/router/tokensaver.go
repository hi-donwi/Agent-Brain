package router

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	ansiRegex     = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	multipleEmpty = regexp.MustCompile(`\n{3,}`)
)

// CompressToolResult compresses tool output text (git diff, grep, logs) to minimize token consumption.
func CompressToolResult(content string) string {
	if len(content) == 0 {
		return content
	}

	// 1. Strip ANSI escape codes
	cleaned := ansiRegex.ReplaceAllString(content, "")

	// 2. Normalize CRLF to LF
	cleaned = strings.ReplaceAll(cleaned, "\r\n", "\n")

	// 3. Trim trailing whitespace per line and collapse repeated identical lines
	lines := strings.Split(cleaned, "\n")
	var result []string

	lastLine := ""
	repeatCount := 0

	flushRepeats := func() {
		if repeatCount > 3 {
			result = append(result, lastLine)
			result = append(result, fmt.Sprintf("...[x%d]...", repeatCount))
		} else {
			for i := 0; i < repeatCount; i++ {
				result = append(result, lastLine)
			}
		}
		repeatCount = 0
	}

	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, " \t")

		if line == lastLine && line != "" {
			repeatCount++
			continue
		}

		flushRepeats()
		result = append(result, line)
		lastLine = line
	}
	flushRepeats()

	joined := strings.Join(result, "\n")

	// 4. Collapse 3+ consecutive newlines into 2
	joined = multipleEmpty.ReplaceAllString(joined, "\n\n")

	return strings.TrimSpace(joined)
}
