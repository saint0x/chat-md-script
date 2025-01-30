# DeepSeek Markdown Chat Monitor

A Rust-based file monitoring system that watches a markdown file for changes and automatically sends new messages to the DeepSeek API, creating an interactive chat experience.

## Features

- Real-time markdown file monitoring
- Automatic message detection and parsing
- Efficient context management (keeps last 6 messages)
- Colored console output with emoji indicators
- Robust error handling
- Memory-safe implementation
- Asynchronous I/O operations

## Setup

1. Install Rust and Cargo
2. Create a `.env` file with your DeepSeek API key:
   ```
   DEEPSEEK_API_KEY=your_api_key_here
   ```
3. Build the project:
   ```bash
   cargo build
   ```

## Usage

1. Run the monitor:
   ```bash
   cargo run
   ```
2. Edit `chat.md` to add your messages
3. Press Enter twice to send a message
4. The AI response will be automatically appended to the file

## Message Format

- Messages are separated by `\n***\n`
- User messages are detected automatically
- AI responses are appended with the separator
- Double newline triggers message sending

## Development

Built with:
- `tokio` for async runtime
- `notify` for file system monitoring
- `reqwest` for API calls
- `serde` for JSON handling
- `anyhow` for error handling
- `colored` for terminal output
