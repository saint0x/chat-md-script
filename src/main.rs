use anyhow::{Context, Result};
use notify::{Config, Event, RecommendedWatcher, RecursiveMode, Watcher};
use serde::{Deserialize, Serialize};
use std::{
    path::Path,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Mutex,
    },
    time::{Duration, Instant},
};
use tokio::{fs, sync::mpsc};

const CHAT_FILE: &str = "chat.md";
const API_URL: &str = "https://api.deepseek.com/v1/chat/completions";
const MAX_CONTEXT_MESSAGES: usize = 6;
const MESSAGE_SEPARATOR: &str = "\n***\n";
const DOUBLE_NEWLINE: &str = "\n\n";

#[derive(Debug, Clone, Serialize, Deserialize)]
struct Message {
    role: String,
    content: String,
}

#[derive(Debug, Serialize)]
struct ApiRequest {
    model: String,
    messages: Vec<Message>,
}

#[derive(Debug, Deserialize)]
struct ApiResponse {
    choices: Vec<Choice>,
}

#[derive(Debug, Deserialize)]
struct Choice {
    message: Message,
}

#[derive(Debug)]
struct ChatContext {
    max_messages: usize,
}

impl ChatContext {
    fn new(_content: String) -> Self {
        Self {
            max_messages: MAX_CONTEXT_MESSAGES,
        }
    }

    fn parse_messages(&self, content: &str) -> Vec<Message> {
        let parts: Vec<&str> = content.split(MESSAGE_SEPARATOR).collect();
        let mut messages = Vec::with_capacity(parts.len());

        for (i, part) in parts.iter().enumerate() {
            let part = part.trim();
            if part.is_empty() {
                continue;
            }

            let role = if i % 2 == 0 { "user" } else { "assistant" };
            messages.push(Message {
                role: role.to_string(),
                content: part.to_string(),
            });
        }

        if messages.len() > self.max_messages {
            messages[messages.len() - self.max_messages..].to_vec()
        } else {
            messages
        }
    }

    fn is_last_message_from_ai(&self, content: &str, cursor_pos: usize) -> bool {
        // Get content up to cursor
        let content_to_cursor = &content[..cursor_pos];
        
        // Find the last separator before cursor
        if let Some(last_sep) = content_to_cursor.rfind(MESSAGE_SEPARATOR) {
            // Get everything between the last separator and cursor
            let after_sep = content_to_cursor[last_sep + MESSAGE_SEPARATOR.len()..].trim();
            
            // If there's no content after separator up to cursor, it was an AI message
            // (because AI messages end with the separator)
            after_sep.is_empty()
        } else {
            // If no separator found before cursor, it's a user message
            false
        }
    }

    fn extract_new_message(&self, content: &str, cursor_pos: usize) -> String {
        let content_to_cursor = &content[..cursor_pos];
        
        // Find the last separator before cursor
        if let Some(last_sep) = content_to_cursor.rfind(MESSAGE_SEPARATOR) {
            // Get everything after the last separator up to cursor
            let message = content_to_cursor[last_sep + MESSAGE_SEPARATOR.len()..].trim();
            if !message.is_empty() {
                return message.to_string();
            }
            
            // If empty after last separator, try to get the content before it
            // (handles case where user is typing right after an AI message)
            if let Some(second_last_sep) = content_to_cursor[..last_sep].rfind(MESSAGE_SEPARATOR) {
                content_to_cursor[second_last_sep + MESSAGE_SEPARATOR.len()..last_sep].trim().to_string()
            } else {
                content_to_cursor[..last_sep].trim().to_string()
            }
        } else {
            // No separator found, use all content up to cursor
            content_to_cursor.trim().to_string()
        }
    }
}

struct ApiClient {
    client: reqwest::Client,
    api_key: String,
}

impl ApiClient {
    fn new(api_key: String) -> Self {
        Self {
            client: reqwest::Client::builder()
                .timeout(Duration::from_secs(30))
                .build()
                .expect("Failed to create HTTP client"),
            api_key,
        }
    }

    async fn call_api(&self, messages: Vec<Message>) -> Result<String> {
        let request = ApiRequest {
            model: "deepseek-chat".to_string(),
            messages,
        };

        let response = self
            .client
            .post(API_URL)
            .header("Authorization", format!("Bearer {}", self.api_key))
            .header("Content-Type", "application/json")
            .json(&request)
            .send()
            .await?;

        if !response.status().is_success() {
            anyhow::bail!("API error: status {}", response.status());
        }

        let api_resp: ApiResponse = response.json().await?;
        api_resp
            .choices
            .first()
            .map(|c| c.message.content.clone())
            .context("No response from API")
    }
}

fn debug_log(message: &str) {
    use colored::Colorize;
    
    let prefixes = [
        ("error", ("❌", "red")),
        ("skip", ("⏭️", "yellow")),
        ("parse", ("🔍", "cyan")),
        ("add", ("➕", "green")),
        ("call", ("🌐", "blue")),
        ("response", ("✉️", "magenta")),
        ("detect", ("👀", "cyan")),
        ("write", ("✍️", "green")),
        ("init", ("🚀", "green")),
        ("load", ("📂", "blue")),
        ("trim", ("✂️", "yellow")),
        ("unchanged", ("🔄", "yellow")),
        ("monitoring", ("👁️", "cyan")),
    ];

    let (prefix, color) = prefixes
        .iter()
        .find(|(key, _)| message.to_lowercase().contains(key))
        .map(|(_, (emoji, color))| (*emoji, *color))
        .unwrap_or(("💬", "white"));

    let colored_message = match color {
        "red" => message.red(),
        "yellow" => message.yellow(),
        "cyan" => message.cyan(),
        "green" => message.green(),
        "blue" => message.blue(),
        "magenta" => message.magenta(),
        _ => message.white(),
    };

    println!("{} {}", prefix, colored_message);
}

async fn process_new_messages(
    content: String,
    last_content: Arc<Mutex<String>>,
    api_client: Arc<ApiClient>,
    chat_context: Arc<Mutex<ChatContext>>,
) -> Result<()> {
    let mut last_content = last_content.lock().unwrap();
    
    if content == *last_content {
        debug_log("unchanged: no new content");
        return Ok(());
    }

    if !content.ends_with(DOUBLE_NEWLINE) {
        debug_log("skip: waiting for double enter");
        *last_content = content;
        return Ok(());
    }

    let cursor_pos = content
        .rfind(DOUBLE_NEWLINE)
        .context("Invalid content format")?;

    let chat_context = chat_context.lock().unwrap();
    
    if chat_context.is_last_message_from_ai(&content, cursor_pos) {
        debug_log("skip: last message was from AI");
        *last_content = content.clone();
        return Ok(());
    }

    let message_content = chat_context.extract_new_message(&content, cursor_pos);
    if message_content.is_empty() {
        debug_log("skip: empty message");
        *last_content = content;
        return Ok(());
    }

    let mut messages = if let Some(last_sep_idx) = content[..cursor_pos].rfind(MESSAGE_SEPARATOR) {
        let prev_content = &content[..last_sep_idx];
        chat_context.parse_messages(prev_content)
    } else {
        Vec::new()
    };

    messages.push(Message {
        role: "user".to_string(),
        content: message_content.clone(),
    });

    debug_log(&format!("parse: sending message: {:?}", message_content));

    // Call API
    debug_log(&format!("call: sending request with {} messages", messages.len()));
    let response = api_client.call_api(messages).await?;

    // Append response
    debug_log("write: adding assistant response");
    let response_text = format!("\n{}{}", response, MESSAGE_SEPARATOR);
    fs::write(CHAT_FILE, format!("{}{}", content, response_text)).await?;

    *last_content = fs::read_to_string(CHAT_FILE).await?;
    Ok(())
}

#[tokio::main]
async fn main() -> Result<()> {
    dotenv::dotenv().ok();

    let api_key = std::env::var("DEEPSEEK_API_KEY").context("DEEPSEEK_API_KEY not found")?;
    let initial_content = fs::read_to_string(CHAT_FILE).await.unwrap_or_default();

    let api_client = Arc::new(ApiClient::new(api_key));
    let chat_context = Arc::new(Mutex::new(ChatContext::new(initial_content.clone())));
    let last_content = Arc::new(Mutex::new(initial_content));

    let (tx, mut rx) = mpsc::channel(10);
    let running = Arc::new(AtomicBool::new(true));
    let running_clone = running.clone();

    let mut watcher = RecommendedWatcher::new(
        move |res: Result<Event, notify::Error>| {
            if let Ok(event) = res {
                if event.kind.is_modify() {
                    let _ = tx.blocking_send(());
                }
            }
        },
        Config::default(),
    )?;

    watcher.watch(Path::new(CHAT_FILE).as_ref(), RecursiveMode::NonRecursive)?;

    debug_log("init: chat monitor started");
    println!("Monitoring chat.md for new messages...");
    println!("Type your message and press Enter twice to send.");

    let mut last_event_time = Instant::now();
    while running.load(Ordering::SeqCst) {
        tokio::select! {
            Some(()) = rx.recv() => {
                if last_event_time.elapsed() < Duration::from_millis(50) {
                    continue;
                }
                last_event_time = Instant::now();

                debug_log("detect: file change");
                let content = fs::read_to_string(CHAT_FILE).await?;
                if let Err(e) = process_new_messages(
                    content,
                    last_content.clone(),
                    api_client.clone(),
                    chat_context.clone(),
                ).await {
                    debug_log(&format!("error: {}", e));
                }
            }
            _ = tokio::signal::ctrl_c() => {
                debug_log("Shutting down...");
                running_clone.store(false, Ordering::SeqCst);
                break;
            }
        }
    }

    Ok(())
}
