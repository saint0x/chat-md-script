package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/joho/godotenv"
)

const (
	chatFile = "chat.md"
	apiURL   = "https://api.deepseek.com/v1/chat/completions"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type APIRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

type APIResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

var lastContent string

func debugLog(format string, args ...interface{}) {
	// Map of prefixes to use based on common keywords in the format string
	prefixes := map[string]string{
		"error":      "❌ ",
		"skip":       "⏭️ ",
		"parse":      "🔍 ",
		"add":        "➕ ",
		"call":       "🌐 ",
		"response":   "✉️ ",
		"detect":     "👀 ",
		"write":      "✍️ ",
		"init":       "🚀 ",
		"load":       "📂 ",
		"trim":       "✂️ ",
		"unchanged":  "🔄 ",
		"monitoring": "👁️ ",
	}

	// Find the most appropriate prefix
	prefix := "💬 " // default prefix
	for key, emoji := range prefixes {
		if strings.Contains(strings.ToLower(format), key) {
			prefix = emoji
			break
		}
	}

	fmt.Printf(prefix+format+"\n", args...)
}

func main() {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file")
	}

	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		log.Fatal("DEEPSEEK_API_KEY not found in environment variables")
	}

	// Create watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	// Initialize last content
	if content, err := os.ReadFile(chatFile); err == nil {
		lastContent = string(content)
		debugLog("load: initial content loaded")
	}

	// Start watching chat.md
	if err := watcher.Add(chatFile); err != nil {
		log.Fatal(err)
	}

	fmt.Println("Monitoring chat.md for new messages...")
	fmt.Println("Type your message and press Enter twice to send.")

	for {
		select {
		case event := <-watcher.Events:
			if event.Op&fsnotify.Write == fsnotify.Write {
				debugLog("detect: file change")
				processNewMessages(apiKey)
			}
		case err := <-watcher.Errors:
			log.Println("Error:", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func parseMessages(content string) []Message {
	var messages []Message

	// Split content by message separator
	parts := strings.Split(content, "\n***\n")
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Even parts are user messages, odd parts are AI responses
		if i%2 == 0 {
			messages = append(messages, Message{
				Role:    "user",
				Content: part,
			})
		} else {
			messages = append(messages, Message{
				Role:    "assistant",
				Content: part,
			})
		}
	}

	// Keep only the last 6 messages for context
	if len(messages) > 6 {
		messages = messages[len(messages)-6:]
		debugLog("trim: keeping last 6 messages for context")
	}

	return messages
}

func processNewMessages(apiKey string) {
	content, err := os.ReadFile(chatFile)
	if err != nil {
		debugLog("error: failed to read chat file: %v", err)
		return
	}

	currentContent := string(content)
	if currentContent == lastContent {
		debugLog("unchanged: no new content")
		return
	}

	// Check for double newline at the end
	if !strings.HasSuffix(currentContent, "\n\n") {
		debugLog("skip: waiting for double enter")
		lastContent = currentContent
		return
	}

	// Get the content up to the last double newline (where cursor is)
	lastIndex := strings.LastIndex(currentContent, "\n\n")
	if lastIndex == -1 {
		debugLog("skip: invalid content format")
		lastContent = currentContent
		return
	}

	// Find the last separator before the cursor position
	contentBeforeCursor := currentContent[:lastIndex]
	lastSepIndex := strings.LastIndex(contentBeforeCursor, "\n***\n")

	var messageContent string
	if lastSepIndex == -1 {
		// No previous messages, use everything up to cursor
		messageContent = strings.TrimSpace(contentBeforeCursor)
	} else {
		// Get everything between last separator and cursor
		messageContent = strings.TrimSpace(contentBeforeCursor[lastSepIndex+5:])
	}

	if messageContent == "" {
		debugLog("skip: empty message")
		lastContent = currentContent
		return
	}

	// Create messages array with the new message
	var messages []Message
	if lastSepIndex != -1 {
		// Get previous messages for context
		prevContent := currentContent[:lastSepIndex]
		prevMessages := parseMessages(prevContent)
		// Keep only the last 5 messages to make room for the new one
		if len(prevMessages) > 5 {
			prevMessages = prevMessages[len(prevMessages)-5:]
			debugLog("trim: keeping last 5 previous messages for context")
		}
		messages = append(messages, prevMessages...)
	}

	// Add the new message
	messages = append(messages, Message{
		Role:    "user",
		Content: messageContent,
	})

	debugLog("parse: sending message: %q", messageContent)
	handleNewMessage(messages, apiKey)
}

func handleNewMessage(messages []Message, apiKey string) {
	// Call API with context
	debugLog("call: sending request with %d messages", len(messages))
	response, err := callDeepSeekAPI(apiKey, messages)
	if err != nil {
		debugLog("error: API call failed: %v", err)
		return
	}

	// Append response
	debugLog("write: adding assistant response")
	if err := appendToChat(response, true); err != nil {
		debugLog("error: failed to write response: %v", err)
		return
	}

	// Update last content
	if content, err := os.ReadFile(chatFile); err == nil {
		lastContent = string(content)
	}
}

func callDeepSeekAPI(apiKey string, messages []Message) (string, error) {
	request := APIRequest{
		Model:    "deepseek-chat",
		Messages: messages,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var apiResp APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return "", err
	}

	if len(apiResp.Choices) == 0 {
		return "", fmt.Errorf("no response from API")
	}

	return apiResp.Choices[0].Message.Content, nil
}

func appendToChat(message string, isAssistant bool) error {
	file, err := os.OpenFile(chatFile, os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	// Get file info to find size
	fileInfo, err := file.Stat()
	if err != nil {
		return err
	}

	// Move cursor to end of file
	if _, err := file.Seek(fileInfo.Size(), 0); err != nil {
		return err
	}

	// Format message with separator if it's an AI response
	var formattedMessage string
	if isAssistant {
		formattedMessage = fmt.Sprintf("\n%s\n***\n", message)
	} else {
		formattedMessage = fmt.Sprintf("\n%s\n", message)
	}

	// Write the message
	if _, err := file.WriteString(formattedMessage); err != nil {
		return err
	}

	return nil
}
