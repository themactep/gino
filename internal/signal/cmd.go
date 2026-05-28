package signal

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// NewSendCmd returns a cobra command that sends a signal to a picobot Unix socket.
// Usage: picobot signal send --type agentchat.message --content "You have a new message"
func NewSendCmd() *cobra.Command {
	var (
		sigType    string
		sigContent string
		sigChannel string
		sigChatID  string
		socketPath string
	)

	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send an external signal to a running picobot gateway",
		Run: func(cmd *cobra.Command, args []string) {
			if sigContent == "" {
				fmt.Fprintln(os.Stderr, "content is required (--content or -m)")
				os.Exit(1)
			}

			sig := Signal{
				Type:    sigType,
				Content: sigContent,
				Channel: sigChannel,
				ChatID:  sigChatID,
			}

			if err := SendSignal(socketPath, sig); err != nil {
				fmt.Fprintf(os.Stderr, "failed to send signal: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("Signal sent successfully")
		},
	}

	cmd.Flags().StringVarP(&sigType, "type", "t", "generic", "Signal type (e.g., agentchat.message)")
	cmd.Flags().StringVarP(&sigContent, "content", "m", "", "Message content (required)")
	cmd.Flags().StringVarP(&sigChannel, "channel", "c", "", "Target channel (e.g., telegram, discord)")
	cmd.Flags().StringVarP(&sigChatID, "chat-id", "", "", "Target chat ID")
	cmd.Flags().StringVarP(&socketPath, "socket", "s", "", "Unix socket path (default: ~/.picobot/signals.sock)")

	return cmd
}

// FormatSignalJSON returns a pretty-printed JSON representation of a signal.
func FormatSignalJSON(sig Signal) string {
	b, _ := json.MarshalIndent(sig, "", "  ")
	return string(b)
}
