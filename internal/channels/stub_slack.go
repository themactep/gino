//go:build only_telegram || only_discord || only_whatsapp

package channels

import (
	"context"
	"log"

	"github.com/local/picobot/internal/chat"
)

func StartSlack(ctx context.Context, hub *chat.Hub, appToken, botToken string, allowUsers []string, allowChannels []string) error {
	log.Println("slack: channel not compiled into this binary (built with single-channel tag).")
	return nil
}
