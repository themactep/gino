//go:build lite || only_telegram || only_discord || only_slack

package channels

import (
	"context"
	"fmt"
	"log"

	"github.com/local/picobot/internal/chat"
)

// StartWhatsApp is a no-op stub used when the binary is built without
// WhatsApp support. If WhatsApp is enabled in the config it logs a clear
// warning and returns nil so the gateway continues with other channels.
func StartWhatsApp(ctx context.Context, hub *chat.Hub, dbPath string, allowFrom []string) error {
	log.Println("whatsapp: channel not compiled into this binary.")
	return nil
}

// SetupWhatsApp returns an error explaining how to build with WhatsApp support.
func SetupWhatsApp(dbPath string) error {
	return fmt.Errorf("WhatsApp support is not compiled into this binary\n" +
		"Build without single-channel tags to include all channels")
}
