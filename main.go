package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	defaultModel        = "openai"
	defaultEndpoint     = "https://text.pollinations.ai/openai"
	discordMaxMsgLen    = 2000
	contextMessageCount = 10
	rateLimitInterval   = 30 * time.Second
	queueBufferSize     = 100
	defaultSystemPrompt = "You are Klaus, a regular user on this Discord server and not a formal AI assistant. " +
		"Keep your replies extremely short, dry, and casual in a typical Discord chat style (usually just a few words or at most 1-2 short sentences, lowercase is fine). " +
		"Never write long paragraphs, bulleted lists, or generic AI filler phrases. " +
		"Naturally match the language, tone, and topic of the ongoing conversation."
)

type pollinationsClient struct {
	apiKey       string
	endpoint     string
	model        string
	systemPrompt string
	httpClient   *http.Client
	lastRequest  time.Time
}

type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func (c *pollinationsClient) generateReply(ctx context.Context, history []chatMessage) (string, error) {
	messages := make([]chatMessage, 0, len(history)+1)
	if c.systemPrompt != "" {
		messages = append(messages, chatMessage{
			Role:    "system",
			Content: c.systemPrompt,
		})
	}
	messages = append(messages, history...)

	reqBody, err := json.Marshal(chatCompletionRequest{
		Model:    c.model,
		Messages: messages,
	})
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	var completion chatCompletionResponse
	if err := json.Unmarshal(bodyBytes, &completion); err == nil {
		if completion.Error != nil {
			return "", fmt.Errorf("API error (%s): %s", completion.Error.Type, completion.Error.Message)
		}
		if len(completion.Choices) > 0 {
			return completion.Choices[0].Message.Content, nil
		}
	}

	// Fallback if Pollinations returns plain text directly
	plainText := strings.TrimSpace(string(bodyBytes))
	if plainText == "" {
		return "", fmt.Errorf("empty response from Pollinations")
	}
	return plainText, nil
}

func main() {
	discordToken := strings.TrimSpace(os.Getenv("KLAUS_DISCORD_BOT_TOKEN"))
	pollinationsAPIKey := strings.TrimSpace(os.Getenv("KLAUS_POLLINATIONS_API_KEY"))
	modelName := strings.TrimSpace(os.Getenv("KLAUS_POLLINATIONS_MODEL"))
	if modelName == "" {
		modelName = defaultModel
	}
	endpoint := strings.TrimSpace(os.Getenv("KLAUS_POLLINATIONS_URL"))
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	systemPrompt := strings.TrimSpace(os.Getenv("KLAUS_SYSTEM_PROMPT"))
	if systemPrompt == "" {
		systemPrompt = defaultSystemPrompt
	}

	if discordToken == "" {
		log.Fatal("Environment variable KLAUS_DISCORD_BOT_TOKEN is missing!")
	}

	ctx := context.Background()

	// Initialize Pollinations client
	aiClient := &pollinationsClient{
		apiKey:       pollinationsAPIKey,
		endpoint:     endpoint,
		model:        modelName,
		systemPrompt: systemPrompt,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}

	// Create discord session
	dg, err := discordgo.New("Bot " + discordToken)
	if err != nil {
		log.Fatalf("Error creating Discord session: %v", err)
	}

	// Set discord intents
	dg.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentsDirectMessages |
		discordgo.IntentsMessageContent

	msgQueue := make(chan *discordgo.MessageCreate, queueBufferSize)
	go processQueue(ctx, dg, aiClient, msgQueue)

	// Event handler for triggering with every new message
	dg.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		enqueueMessage(s, m, msgQueue)
	})

	// Open websocket to discord
	if err := dg.Open(); err != nil {
		log.Fatalf("Error connecting to Discord: %v", err)
	}
	defer dg.Close()

	fmt.Printf("Klaus is online (User: %s, Modell: %s). Press CTRL+C to stop.\n", dg.State.User.Username, modelName)

	// wait until CTRL+C (SIGINT) or SIGTERM
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-stop

	fmt.Println("\nKlaus is shutting down...")
}

func enqueueMessage(
	s *discordgo.Session,
	m *discordgo.MessageCreate,
	msgQueue chan<- *discordgo.MessageCreate,
) {
	// Ignore own messages
	if s.State.User == nil || m.Author.ID == s.State.User.ID {
		return
	}

	botID := s.State.User.ID
	if !isBotMentioned(m, botID) {
		return
	}

	// Remove ping from the message
	prompt := stripBotMention(m.Content, botID)
	if prompt == "" && m.ReferencedMessage == nil {
		_, _ = s.ChannelMessageSendReply(m.ChannelID, "was", m.Reference())
		return
	}

	select {
	case msgQueue <- m:
	default:
		log.Printf("Queue is full, dropping message %s", m.ID)
	}
}

func processQueue(
	ctx context.Context,
	s *discordgo.Session,
	aiClient *pollinationsClient,
	msgQueue <-chan *discordgo.MessageCreate,
) {
	for m := range msgQueue {
		if !aiClient.lastRequest.IsZero() {
			elapsed := time.Since(aiClient.lastRequest)
			if elapsed < rateLimitInterval {
				wait := rateLimitInterval - elapsed
				log.Printf("Rate-Limit Queue: waiting %v before responding to %s...", wait.Round(time.Second), m.Author.Username)
				time.Sleep(wait)
			}
		}

		handleQueuedMessage(ctx, s, m, aiClient)
		aiClient.lastRequest = time.Now()
	}
}

func handleQueuedMessage(
	ctx context.Context,
	s *discordgo.Session,
	m *discordgo.MessageCreate,
	aiClient *pollinationsClient,
) {
	botID := s.State.User.ID
	prompt := stripBotMention(m.Content, botID)

	// Show "Klaus is typing..." while thinking
	_ = s.ChannelTyping(m.ChannelID)

	history := buildChatHistory(s, m, botID, prompt)

	// Send request to Pollinations API (with 1 automatic retry if HTTP 429 occurs)
	respText, err := aiClient.generateReply(ctx, history)
	if err != nil && strings.Contains(err.Error(), "HTTP 429") {
		log.Printf("Pollinations 429 received, waiting %v and trying again...", rateLimitInterval)
		time.Sleep(rateLimitInterval)
		_ = s.ChannelTyping(m.ChannelID)
		respText, err = aiClient.generateReply(ctx, history)
	}
	if err != nil {
		log.Printf("Pollinations error: %v", err)
		_, _ = s.ChannelMessageSendReply(m.ChannelID, "kein plan", m.Reference())
		return
	}

	replyText := strings.TrimSpace(respText)
	if replyText == "" {
		replyText = ""
	}

	// Send reply in discord, split if necessary (Discord allows max. 2000 characters per message)
	for i, chunk := range splitMessage(replyText, discordMaxMsgLen) {
		var sendErr error
		if i == 0 {
			_, sendErr = s.ChannelMessageSendReply(m.ChannelID, chunk, m.Reference())
		} else {
			_, sendErr = s.ChannelMessageSend(m.ChannelID, chunk)
		}
		if sendErr != nil {
			log.Printf("Error sending message on discord: %v", sendErr)
			return
		}
	}
}

func buildChatHistory(s *discordgo.Session, m *discordgo.MessageCreate, botID, currentPrompt string) []chatMessage {
	var history []chatMessage

	// Fetch recent channel messages before the current one as background context only
	recentMsgs, err := s.ChannelMessages(m.ChannelID, contextMessageCount, m.ID, "", "")
	if err == nil && len(recentMsgs) > 0 {
		var transcriptLines []string
		// ChannelMessages returns newest first; iterate in reverse for chronological order
		for i := len(recentMsgs) - 1; i >= 0; i-- {
			msg := recentMsgs[i]
			text := stripBotMention(msg.Content, botID)
			if text == "" || msg.Author == nil {
				continue
			}
			authorName := msg.Author.Username
			if msg.Author.ID == botID {
				authorName = "Klaus (you)"
			}
			transcriptLines = append(transcriptLines, fmt.Sprintf("%s: %s", authorName, text))
		}
		if len(transcriptLines) > 0 {
			history = append(history, chatMessage{
				Role: "system",
				Content: "Recent channel chat log for background context only (do NOT answer old questions from this log; only respond to the user message that follows):\n" +
					strings.Join(transcriptLines, "\n"),
			})
		}
	}

	if currentPrompt == "" {
		currentPrompt = "(refers to the previous/referenced message)"
	}

	userContent := fmt.Sprintf("%s: %s", m.Author.Username, currentPrompt)
	if m.ReferencedMessage != nil && m.ReferencedMessage.Author != nil {
		refText := stripBotMention(m.ReferencedMessage.Content, botID)
		if refText != "" {
			userContent = fmt.Sprintf("[Replying to %s: %q]\n%s", m.ReferencedMessage.Author.Username, refText, userContent)
		}
	}

	history = append(history, chatMessage{
		Role:    "user",
		Content: userContent,
	})

	return history
}

func isBotMentioned(m *discordgo.MessageCreate, botID string) bool {
	for _, user := range m.Mentions {
		if user.ID == botID {
			return true
		}
	}
	return false
}

func stripBotMention(content, botID string) string {
	content = strings.ReplaceAll(content, "<@"+botID+">", "")
	content = strings.ReplaceAll(content, "<@!"+botID+">", "")
	return strings.TrimSpace(content)
}

func splitMessage(text string, maxLen int) []string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(runes) > maxLen {
		chunks = append(chunks, string(runes[:maxLen]))
		runes = runes[maxLen:]
	}
	if len(runes) > 0 {
		chunks = append(chunks, string(runes))
	}
	return chunks
}
