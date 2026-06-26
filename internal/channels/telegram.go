//go:build !only_discord && !only_slack && !only_whatsapp

package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wltechblog/gino/internal/chat"
	"sync"
)

const (
	tgMaxRetries     = 3
	tgRetryBaseDelay = 2 * time.Second
	tgMaxMessageLen  = 4096 // Telegram sendMessage limit
	tgMaxCaptionLen  = 1024 // Telegram sendDocument caption limit
)

// redactToken removes the bot token from a Telegram API URL for safe logging.
// e.g. "https://api.telegram.org/bot123:ABC/sendMessage" → "https://api.telegram.org/bot***/sendMessage"
func redactToken(s string) string {
	const prefix = "https://api.telegram.org/bot"
	if strings.HasPrefix(s, prefix) {
		rest := s[len(prefix):]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			return prefix + "***" + rest[slash:]
		}
	}
	return s
}

// retryPostForm retries PostForm calls with exponential backoff.
func retryPostForm(client *http.Client, apiURL string, data url.Values) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < tgMaxRetries; attempt++ {
		if attempt > 0 {
			delay := tgRetryBaseDelay * time.Duration(1<<(attempt-1))
			log.Printf("telegram: retry %d/%d after %v for %s", attempt, tgMaxRetries, delay, redactToken(apiURL))
			time.Sleep(delay)
		}
		resp, err := client.PostForm(apiURL, data)
		if err != nil {
			lastErr = err
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("telegram: %d retries exhausted: %w", tgMaxRetries, lastErr)
}

// retryPost retries Post calls with exponential backoff.
func retryPost(client *http.Client, apiURL, contentType string, body *bytes.Buffer) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < tgMaxRetries; attempt++ {
		if attempt > 0 {
			delay := tgRetryBaseDelay * time.Duration(1<<(attempt-1))
			log.Printf("telegram: retry %d/%d after %v for %s", attempt, tgMaxRetries, delay, redactToken(apiURL))
			time.Sleep(delay)
		}
		resp, err := client.Post(apiURL, contentType, bytes.NewReader(body.Bytes()))
		if err != nil {
			lastErr = err
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("telegram: %d retries exhausted: %w", tgMaxRetries, lastErr)
}

func StartTelegram(ctx context.Context, hub *chat.Hub, token string, allowFrom []string, showTyping bool, workspace string) error {
	if token == "" {
		return fmt.Errorf("telegram token not provided")
	}
	base := "https://api.telegram.org/bot" + token
	return StartTelegramWithBase(ctx, hub, token, base, allowFrom, showTyping, workspace)
}

func StartTelegramWithBase(ctx context.Context, hub *chat.Hub, token, base string, allowFrom []string, showTyping bool, workspace string) error {
	if base == "" {
		return fmt.Errorf("base URL is required")
	}

	allowed := make(map[string]struct{}, len(allowFrom))
	for _, id := range allowFrom {
		allowed[id] = struct{}{}
	}

	client := &http.Client{Timeout: 45 * time.Second}
	fileBase := strings.Replace(base, "/bot"+token, "/file/bot"+token, 1)

	typingMu := new(sync.Mutex)
	typingChats := make(map[string]struct{})
	typingDone := make(map[string]chan struct{})

	startTyping := func(chatID string) {
		typingMu.Lock()
		if _, exists := typingChats[chatID]; exists {
			typingMu.Unlock()
			return
		}
		typingChats[chatID] = struct{}{}
		done := make(chan struct{})
		typingDone[chatID] = done
		typingMu.Unlock()
		go func() {
			defer func() {
				typingMu.Lock()
				delete(typingChats, chatID)
				delete(typingDone, chatID)
				typingMu.Unlock()
			}()
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				v := url.Values{}
				v.Set("chat_id", chatID)
				v.Set("action", "typing")
				resp, err := retryPostForm(client, base+"/sendChatAction", v)
				if err != nil {
					log.Printf("telegram sendChatAction error: %v", err)
				} else {
					io.ReadAll(resp.Body)
					resp.Body.Close()
				}
				select {
				case <-done:
					return
				case <-ticker.C:
				}
			}
		}()
	}

	stopTyping := func(chatID string) {
		typingMu.Lock()
		if done, ok := typingDone[chatID]; ok {
			close(done)
		}
		typingMu.Unlock()
	}

	go func() {
		offset := int64(0)
		for {
			select {
			case <-ctx.Done():
				log.Println("telegram: stopping inbound polling")
				return
			default:
			}

			values := url.Values{}
			values.Set("offset", strconv.FormatInt(offset, 10))
			values.Set("timeout", "30")
			resp, err := client.PostForm(base+"/getUpdates", values)
			if err != nil {
				log.Printf("telegram getUpdates error: %v", err)
				time.Sleep(1 * time.Second)
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				log.Printf("telegram getUpdates: HTTP %d — backing off", resp.StatusCode)
				if resp.StatusCode == 401 {
					log.Printf("telegram getUpdates: 401 Unauthorized — token may be invalid")
				}
				backoff := 5 * time.Second
				if resp.StatusCode == 429 {
					backoff = 30 * time.Second
				}
				time.Sleep(backoff)
				continue
			}
			var gu struct {
				Ok     bool `json:"ok"`
				Result []struct {
					UpdateID int64 `json:"update_id"`
					Message  *struct {
						MessageID int64 `json:"message_id"`
						From      *struct {
							ID        int64  `json:"id"`
							FirstName string `json:"first_name"`
						} `json:"from"`
						Chat struct {
							ID int64 `json:"id"`
						} `json:"chat"`
						Text     string `json:"text"`
						Caption  string `json:"caption"`
						Document *struct {
							FileID   string `json:"file_id"`
							FileName string `json:"file_name"`
						} `json:"document"`
						Photo []struct {
							FileID   string `json:"file_id"`
							Width    int    `json:"width"`
							Height   int    `json:"height"`
							FileSize int    `json:"file_size"`
						} `json:"photo"`
					} `json:"message"`
				} `json:"result"`
			}
			if err := json.Unmarshal(body, &gu); err != nil {
				log.Printf("telegram: invalid getUpdates response: %v", err)
				continue
			}
			for _, upd := range gu.Result {
				if upd.UpdateID >= offset {
					offset = upd.UpdateID + 1
				}
				if upd.Message == nil {
					continue
				}
				m := upd.Message
				fromID := ""
				if m.From != nil {
					fromID = strconv.FormatInt(m.From.ID, 10)
				}
				if len(allowed) > 0 {
					if _, ok := allowed[fromID]; !ok {
						log.Printf("telegram: dropping message from unauthorized user %s", fromID)
						continue
					}
				}
				chatID := strconv.FormatInt(m.Chat.ID, 10)
				content := m.Text
			if content == "" {
				content = m.Caption
			}
				var media []string

				if m.Document != nil {
					saved, err := tgDownloadFile(client, base, fileBase, m.Document.FileID, m.Document.FileName, chatID, workspace)
					if err != nil {
						log.Printf("telegram: failed to download document: %v", err)
						content += "\n[Failed to download attached file: " + m.Document.FileName + "]"
					} else {
						media = append(media, saved)
						if content == "" {
							content = "[File received: " + m.Document.FileName + "]"
						} else {
							content += "\n[File received: " + m.Document.FileName + "]"
						}
					}
				}

				if len(m.Photo) > 0 {
					photo := m.Photo[len(m.Photo)-1]
					filename := "photo_" + strconv.FormatInt(time.Now().UnixMilli(), 10) + ".jpg"
					saved, err := tgDownloadFile(client, base, fileBase, photo.FileID, filename, chatID, workspace)
					if err != nil {
						log.Printf("telegram: failed to download photo: %v", err)
						content += "\n[Failed to download attached photo]"
					} else {
						media = append(media, saved)
						if content == "" {
							content = "[Photo received]"
						}
					}
				}

				if content == "" && len(media) == 0 {
					continue
				}

			hub.In <- chat.Inbound{
				Channel:   "telegram",
				SenderID:  fromID,
				ChatID:    chatID,
				Content:   content,
				Timestamp: time.Now(),
				Media:     media,
			}
			if showTyping {
				startTyping(chatID)
			}
			}
		}
	}()

	outCh := hub.Subscribe("telegram")

	go func() {
		outClient := &http.Client{Timeout: 60 * time.Second}
		for {
			select {
			case <-ctx.Done():
				log.Println("telegram: stopping outbound sender")
				return
			case out := <-outCh:
				stopTyping(out.ChatID)
				log.Printf("telegram: sending message to %s (%d chars)", out.ChatID, len(out.Content))
				if len(out.Media) > 0 {
					for i, p := range out.Media {
						caption := ""
						if i == 0 {
							caption = truncateCaption(out.Content)
						}
						if err := tgSendDocument(outClient, base, out.ChatID, p, caption); err != nil {
							log.Printf("telegram sendDocument error: %v", err)
						}
					}
					continue
				}
				if err := tgSendChunked(outClient, base, out.ChatID, out.Content); err != nil {
					log.Printf("telegram sendMessage error: %v", err)
					continue
				}
			}
		}
	}()

	return nil
}

func tgDownloadFile(client *http.Client, base, fileBase, fileID, filename, chatID, workspace string) (string, error) {
	filePath, err := tgGetFilePath(client, base, fileID)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(workspace, "uploads", chatID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, filename)

	downloadURL := fileBase + "/" + filePath
	resp, err := client.Get(downloadURL)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: status %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	return dest, nil
}

func tgGetFilePath(client *http.Client, base, fileID string) (string, error) {
	v := url.Values{}
	v.Set("file_id", fileID)
	resp, err := client.PostForm(base+"/getFile", v)
	if err != nil {
		return "", fmt.Errorf("getFile: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result struct {
		OK   bool `json:"ok"`
		File struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("getFile parse: %w", err)
	}
	if !result.OK || result.File.FilePath == "" {
		return "", fmt.Errorf("getFile no path: %s", strings.TrimSpace(string(body)))
	}
	return result.File.FilePath, nil
}

func tgSendDocument(client *http.Client, base, chatID, filePath, caption string) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("chat_id", chatID)
	if caption != "" {
		_ = w.WriteField("caption", caption)
		_ = w.WriteField("parse_mode", "MarkdownV2")
	}
	part, err := w.CreateFormFile("document", filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("form file: %w", err)
	}
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(part, f); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("multipart close: %w", err)
	}
	resp, err := retryPost(client, base+"/sendDocument", w.FormDataContentType(), &buf)
	if err != nil {
		return fmt.Errorf("sendDocument: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == 200 {
		return nil
	}
	if resp.StatusCode == 400 && bytes.Contains(body, []byte("can't parse entities")) {
		log.Printf("telegram: markdown parse error in caption, retrying as plain text")
		return tgSendDocumentPlain(client, base, chatID, filePath, caption)
	}
	return fmt.Errorf("sendDocument: HTTP %d: %s", resp.StatusCode, string(body))
}

func tgSendDocumentPlain(client *http.Client, base, chatID, filePath, caption string) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("chat_id", chatID)
	if caption != "" {
		_ = w.WriteField("caption", caption)
	}
	part, err := w.CreateFormFile("document", filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("form file: %w", err)
	}
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(part, f); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("multipart close: %w", err)
	}
	resp, err := retryPost(client, base+"/sendDocument", w.FormDataContentType(), &buf)
	if err != nil {
		return fmt.Errorf("sendDocument: %w", err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	return nil
}

// truncateCaption trims content to Telegram's caption limit.
func truncateCaption(content string) string {
	if len(content) <= tgMaxCaptionLen {
		return content
	}
	return content[:tgMaxCaptionLen-3] + "…"
}

// tgSendChunked sends a message, splitting it into chunks if it exceeds the Telegram limit.
// Splits on newlines where possible to avoid breaking sentences/mid-word.
func tgSendChunked(client *http.Client, base, chatID, content string) error {
	if len(content) <= tgMaxMessageLen {
		return tgSendMessage(client, base, chatID, content)
	}

	chunks := splitMessage(content, tgMaxMessageLen)
	for i, chunk := range chunks {
		if err := tgSendMessage(client, base, chatID, chunk); err != nil {
			return fmt.Errorf("chunk %d/%d: %w", i+1, len(chunks), err)
		}
		if i < len(chunks)-1 {
			time.Sleep(300 * time.Millisecond) // small delay between chunks
		}
	}
	log.Printf("telegram: sent %d chunks to %s", len(chunks), chatID)
	return nil
}

// isWordChar returns true if c is an alphanumeric character or other
// character that commonly appears inside words (e.g. in identifiers).
func isWordChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// findCloseMarker scans forward from pos+1 to find the matching close marker.
// Returns the index of the close marker, or -1 if not found.
func findCloseMarker(s string, pos int, marker byte) int {
	for j := pos + 1; j < len(s); j++ {
		if s[j] == marker {
			return j
		}
	}
	return -1
}

// findCloseMarkerStr scans forward from pos+len(marker) to find matching close.
// Returns the index of the start of the close marker, or -1 if not found.
func findCloseMarkerStr(s string, pos int, marker string) int {
	mlen := len(marker)
	for j := pos + mlen; j+mlen <= len(s); j++ {
		if s[j:j+mlen] == marker {
			return j
		}
	}
	return -1
}

// tgEscapeText escapes all MarkdownV2 reserved characters in a string.
// Used for content inside formatting spans where every special char must be escaped.
func tgEscapeText(s string) string {
	var b strings.Builder
	b.Grow(len(s) + len(s)/4)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '_', '*', '[', ']', '(', ')', '~', '`', '>', '#', '+', '-', '=', '|', '{', '}', '.', '!':
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// tgEscapeReserved escapes MarkdownV2 reserved characters.
// Telegram requires \ before any of _ * [ ] ( ) ~ ` > # + - = | { } . !
// everywhere except inside code/code-block entities.
//
// Formatting spans (bold, italic, links, etc.) are detected and their
// delimiters preserved, but special chars inside the span content are
// escaped as Telegram requires. Code spans are passed verbatim.
func tgEscapeReserved(s string) string {
	var b strings.Builder
	i := 0
	n := len(s)

	for i < n {
		// Code block ```...``` — preserve verbatim (no escaping inside)
		if i+2 < n && s[i] == '`' && s[i+1] == '`' && s[i+2] == '`' {
			b.WriteString("```")
			i += 3
			for i+2 < n && !(s[i] == '`' && s[i+1] == '`' && s[i+2] == '`') {
				b.WriteByte(s[i])
				i++
			}
			if i+2 < n {
				b.WriteString("```")
				i += 3
			}
			continue
		}

		// Inline code `...` — preserve verbatim (no escaping inside)
		if s[i] == '`' {
			closeIdx := findCloseMarker(s, i, '`')
			if closeIdx > i {
				b.WriteString(s[i : closeIdx+1])
				i = closeIdx + 1
				continue
			}
			b.WriteString("\\`")
			i++
			continue
		}

		// Bold *...* — escape content inside
		if s[i] == '*' && (i+1 >= n || s[i+1] != '*') {
			if i > 0 && isWordChar(s[i-1]) && i+1 < n && isWordChar(s[i+1]) {
				b.WriteString("\\*")
				i++
				continue
			}
			closeIdx := findCloseMarker(s, i, '*')
			if closeIdx > i {
				b.WriteByte('*')
				b.WriteString(tgEscapeText(s[i+1 : closeIdx]))
				b.WriteByte('*')
				i = closeIdx + 1
				continue
			}
			b.WriteString("\\*")
			i++
			continue
		}

		// Underline __...__ — escape content inside
		if i+1 < n && s[i] == '_' && s[i+1] == '_' {
			closeIdx := findCloseMarkerStr(s, i, "__")
			if closeIdx > i {
				b.WriteString("__")
				b.WriteString(tgEscapeText(s[i+2 : closeIdx]))
				b.WriteString("__")
				i = closeIdx + 2
				continue
			}
			b.WriteString("\\_\\_")
			i += 2
			continue
		}

		// Italic _..._ — escape content inside, only at word boundaries
		if s[i] == '_' {
			if i > 0 && isWordChar(s[i-1]) {
				b.WriteString("\\_")
				i++
				continue
			}
			if i+1 < n && !isWordChar(s[i+1]) {
				b.WriteString("\\_")
				i++
				continue
			}
			closeIdx := findCloseMarker(s, i, '_')
			if closeIdx > i {
				b.WriteByte('_')
				b.WriteString(tgEscapeText(s[i+1 : closeIdx]))
				b.WriteByte('_')
				i = closeIdx + 1
				continue
			}
			b.WriteString("\\_")
			i++
			continue
		}

		// Strikethrough ~...~ — escape content inside
		if s[i] == '~' {
			if i+1 < n && s[i+1] == '~' {
				closeIdx := findCloseMarkerStr(s, i, "~~")
				if closeIdx > i {
					b.WriteString("~~")
					b.WriteString(tgEscapeText(s[i+2 : closeIdx]))
					b.WriteString("~~")
					i = closeIdx + 2
					continue
				}
				b.WriteString("\\~\\~")
				i += 2
				continue
			}
			closeIdx := findCloseMarker(s, i, '~')
			if closeIdx > i {
				b.WriteByte('~')
				b.WriteString(tgEscapeText(s[i+1 : closeIdx]))
				b.WriteByte('~')
				i = closeIdx + 1
				continue
			}
			b.WriteString("\\~")
			i++
			continue
		}

		// Spoiler ||...|| — escape content inside
		if i+1 < n && s[i] == '|' && s[i+1] == '|' {
			closeIdx := findCloseMarkerStr(s, i, "||")
			if closeIdx > i {
				b.WriteString("||")
				b.WriteString(tgEscapeText(s[i+2 : closeIdx]))
				b.WriteString("||")
				i = closeIdx + 2
				continue
			}
			b.WriteString("\\|\\|")
			i += 2
			continue
		}

		// Link [text](url) — escape text content, keep URL verbatim
		if s[i] == '[' {
			j := i + 1
			depth := 1
			for j < n && depth > 0 {
				if s[j] == '[' {
					depth++
				} else if s[j] == ']' {
					depth--
				}
				j++
			}
			if depth == 0 {
				closeBracket := j - 1
				if closeBracket+1 < n && s[closeBracket+1] == '(' {
					j = closeBracket + 2
					depth = 1
					for j < n && depth > 0 {
						if s[j] == '(' {
							depth++
						} else if s[j] == ')' {
							depth--
						}
						j++
					}
					if depth == 0 {
						b.WriteByte('[')
						b.WriteString(tgEscapeText(s[i+1 : closeBracket]))
						b.WriteString("](")
						b.WriteString(s[closeBracket+2 : j-1]) // URL verbatim
						b.WriteByte(')')
						i = j
						continue
					}
				}
			}
			b.WriteString("\\[")
			i++
			continue
		}

		// Blockquote > at line start — escape content after marker
		if (i == 0 || s[i-1] == '\n') && s[i] == '>' {
			b.WriteByte('>')
			i++
			if i < n && s[i] == ' ' {
				b.WriteByte(' ')
				i++
			}
			continue
		}

		// Escape reserved character
		switch s[i] {
		case '_', '*', '[', ']', '(', ')', '~', '`', '>', '#', '+', '-', '=', '|', '{', '}', '.', '!':
			b.WriteByte('\\')
			b.WriteByte(s[i])
		default:
			b.WriteByte(s[i])
		}
		i++
	}

	return b.String()
}

// tgSendMessage sends a message with MarkdownV2 formatting.
// Reserved characters are escaped to satisfy Telegram's strict parser
// while preserving intentional markdown formatting spans.
// Falls back to plain text on unhandled parse errors.
func tgSendMessage(client *http.Client, base, chatID, text string) error {
	u := base + "/sendMessage"
	escaped := tgEscapeReserved(text)
	v := url.Values{}
	v.Set("chat_id", chatID)
	v.Set("text", escaped)
	v.Set("parse_mode", "MarkdownV2")
	resp, err := retryPostForm(client, u, v)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == 200 {
		return nil
	}
	if resp.StatusCode == 400 && bytes.Contains(body, []byte("can't parse entities")) {
		log.Printf("telegram: markdown parse error, retrying as plain text")
		v.Set("text", text)
		v.Del("parse_mode")
		resp2, err2 := retryPostForm(client, u, v)
		if err2 != nil {
			return err2
		}
		body2, _ := io.ReadAll(resp2.Body)
		resp2.Body.Close()
		if resp2.StatusCode == 200 {
			return nil
		}
		return fmt.Errorf("HTTP %d: %s", resp2.StatusCode, string(body2))
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
}

