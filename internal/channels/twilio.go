//go:build with_twilio

package channels

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	twilioClient "github.com/twilio/twilio-go/client"
	"github.com/twilio/twilio-go/rest/api/v2010"
	"github.com/wltechblog/gino/internal/chat"
	"github.com/wltechblog/gino/internal/config"
)

const twilioMaxBodyLen = 1600

// StartTwilio starts the Twilio SMS channel.
// It sets up an HTTP webhook server for inbound messages and subscribes
// to the hub for outbound messages.
func StartTwilio(ctx context.Context, hub *chat.Hub, cfg config.TwilioConfig) error {
	if cfg.AccountSID == "" {
		return fmt.Errorf("twilio: account SID not provided")
	}
	if cfg.AuthToken == "" {
		return fmt.Errorf("twilio: auth token not provided")
	}
	if cfg.PhoneNumber == "" {
		return fmt.Errorf("twilio: phone number not provided")
	}

	allowed := make(map[string]struct{}, len(cfg.AllowFrom))
	for _, n := range cfg.AllowFrom {
		allowed[n] = struct{}{}
	}

	port := cfg.WebhookPort
	if port <= 0 {
		port = 8080
	}

	c := &twilioClient.Client{}
	c.SetAccountSid(cfg.AccountSID)
	c.Credentials = twilioClient.NewCredentials(cfg.AccountSID, cfg.AuthToken)

	api := openapi.NewApiServiceWithClient(c)
	validator := twilioClient.NewRequestValidator(cfg.AuthToken)

	outCh := hub.Subscribe("twilio")

	mux := http.NewServeMux()
	mux.HandleFunc("/twilio/sms", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("twilio: failed to read request body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		signature := r.Header.Get("X-Twilio-Signature")
		if signature != "" {
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			fullURL := scheme + "://" + r.Host + r.URL.String()
			if !validator.ValidateBody(fullURL, bodyBytes, signature) {
				log.Printf("twilio: invalid webhook signature")
				http.Error(w, "invalid signature", http.StatusUnauthorized)
				return
			}
		}

		vals, err := url.ParseQuery(string(bodyBytes))
		if err != nil {
			log.Printf("twilio: failed to parse form body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		from := vals.Get("From")
		msgBody := vals.Get("Body")
		if from == "" || msgBody == "" {
			http.Error(w, "missing From or Body", http.StatusBadRequest)
			return
		}

		if len(allowed) > 0 {
			if _, ok := allowed[from]; !ok {
				log.Printf("twilio: dropping message from unauthorized number %s", from)
				w.WriteHeader(http.StatusOK)
				return
			}
		}

		log.Printf("twilio: inbound SMS from %s: %s", from, truncate(msgBody, 50))

		hub.In <- chat.Inbound{
			Channel:   "twilio",
			SenderID:  from,
			ChatID:    from,
			Content:   msgBody,
			Timestamp: time.Now(),
		}

		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	go func() {
		log.Printf("twilio: starting webhook server on :%d — configure your Twilio phone number's incoming webhook to POST http(s)://<public-url>/twilio/sms", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("twilio: webhook server error: %v", err)
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case out := <-outCh:
				log.Printf("twilio: sending SMS to %s (%d chars)", out.ChatID, len(out.Content))
				for _, chunk := range splitMessage(out.Content, twilioMaxBodyLen) {
					if err := sendTwilioSMS(api, cfg.PhoneNumber, out.ChatID, chunk); err != nil {
						log.Printf("twilio: send error: %v", err)
						break
					}
					time.Sleep(200 * time.Millisecond)
				}
			}
		}
	}()

	return nil
}

func sendTwilioSMS(api *openapi.ApiService, from, to, body string) error {
	params := &openapi.CreateMessageParams{}
	params.SetTo(to)
	params.SetFrom(from)
	params.SetBody(body)
	_, err := api.CreateMessage(params)
	return err
}
