# AI Chat in Markdown

A real-time chat interface that allows you to have conversations with DeepSeek API directly in a markdown file. The system monitors your input and automatically sends messages when you press Enter twice.

## Setup

1. Install the required dependencies:
   ```bash
   go mod download
   ```

2. Create a `.env` file in the root directory and add your DeepSeek API key:
   ```
   DEEPSEEK_API_KEY=your_api_key_here
   ```

## Usage

1. Run the monitoring script:
   ```bash
   go run script.go
   ```

2. Open `chat.md` in your favorite text editor
3. Type your message
4. Press Enter twice to send (the script will detect the double-enter and send your message)
5. The AI's response will automatically appear in the file where your cursor is
6. Continue the conversation by typing your next message and pressing Enter twice
