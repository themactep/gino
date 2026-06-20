//go:build !with_twilio

package channels

import (
	"context"
	"log"

	"github.com/wltechblog/gino/internal/chat"
	"github.com/wltechblog/gino/internal/config"
)

func StartTwilio(ctx context.Context, hub *chat.Hub, cfg config.TwilioConfig) error {
	log.Println("twilio: channel not compiled into this binary (build with -tags with_twilio to enable).")
	return nil
}
