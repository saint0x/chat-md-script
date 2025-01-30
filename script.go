package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
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

var (
	humanColor     = color.New(color.FgCyan)
	assistantColor = color.New(color.FgGreen)
	debugColor     = color.New(color.FgYellow)
	lastContent    string
)

func debugLog(format string, args ...interface{}) {
	debugColor.Printf("[DEBUG] "+format+"\n", args...)
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
		debugLog("Initial content loaded, length: %d", len(lastContent))
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
				debugLog("File write detected")
				processNewMessages(apiKey)
			}
		case err := <-watcher.Errors:
			log.Println("Error:", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func processNewMessages(apiKey string) {
	content, err := os.ReadFile(chatFile)
	if err != nil {
		log.Println("Error reading file:", err)
		return
	}

	currentContent := string(content)
	debugLog("Current content length: %d, Last content length: %d", len(currentContent), len(lastContent))

	if currentContent == lastContent {
		debugLog("Content unchanged, skipping")
		return
	}

	// Get the last two characters to check for double newline
	lastTwoChars := ""
	if len(currentContent) >= 2 {
		lastTwoChars = currentContent[len(currentContent)-2:]
	}

	// Only process if we detect a double newline
	if lastTwoChars != "\n\n" {
		debugLog("No double newline detected at the end")
		lastContent = currentContent
		return
	}

	// Check if the last non-empty line is from the assistant (orange color)
	scanner := bufio.NewScanner(strings.NewReader(currentContent))
	var lastNonEmptyLine string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lastNonEmptyLine = line
		}
	}

	if strings.Contains(lastNonEmptyLine, "color: orange") {
		debugLog("Last message is from Assistant, skipping")
		lastContent = currentContent
		return
	}

	// Parse all messages
	messages := parseMessages(currentContent)
	debugLog("Parsed %d messages", len(messages))

	if len(messages) == 0 {
		debugLog("No messages parsed")
		return
	}

	// Get the last message
	lastMessage := messages[len(messages)-1]
	if lastMessage.Role != "user" {
		debugLog("Last message is not from user, skipping")
		lastContent = currentContent
		return
	}

	// Call DeepSeek API
	debugLog("Calling DeepSeek API")
	response, err := callDeepSeekAPI(apiKey, messages)
	if err != nil {
		log.Println("Error calling DeepSeek API:", err)
		return
	}

	// Append response to file at the current cursor position
	debugLog("Got response, appending to file")
	if err := appendToChat(response, true); err != nil {
		log.Println("Error appending response:", err)
		return
	}

	// Update last content after processing
	content, _ = os.ReadFile(chatFile)
	lastContent = string(content)
	debugLog("Updated last content, length: %d", len(lastContent))
}

func parseMessages(content string) []Message {
	var messages []Message
	debugLog("Parsing content: %q", content)
	scanner := bufio.NewScanner(strings.NewReader(content))
	var messageLines []string

	// First, collect all non-empty lines and strip color tags
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			// Strip color tags if present
			if strings.Contains(trimmed, "<span") {
				// Extract message from between span tags
				start := strings.Index(trimmed, ">") + 1
				end := strings.LastIndex(trimmed, "<")
				if start > 0 && end > start {
					trimmed = trimmed[start:end]
				}
			}
			messageLines = append(messageLines, trimmed)
		}
	}

	// Process messages alternating between user and assistant
	isUserMessage := true // Start with user message
	for _, line := range messageLines {
		if isUserMessage {
			messages = append(messages, Message{
				Role:    "user",
				Content: line,
			})
			debugLog("Added user message: %q", line)
		} else {
			messages = append(messages, Message{
				Role:    "assistant",
				Content: line,
			})
			debugLog("Added assistant message: %q", line)
		}
		isUserMessage = !isUserMessage
	}

	return messages
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

	// Format message with markdown color
	var formattedMessage string
	if isAssistant {
		formattedMessage = fmt.Sprintf("\n<span style=\"color: orange\">%s</span>\n", message)
	} else {
		formattedMessage = fmt.Sprintf("\n<span style=\"color: blue\">%s</span>\n", message)
	}

	// Write the message
	if _, err := file.WriteString(formattedMessage); err != nil {
		return err
	}

	return nil
}
